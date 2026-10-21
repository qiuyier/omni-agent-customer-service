import {randomUUID} from 'node:crypto';

// Business-neutral protocol. Adapters own authentication, transport and resources.
// No open-traffic operation exists. Exceptions and remote bodies never enter reports.
export async function acceptRestore(adapter, {id = randomUUID()} = {}) {
  const report = {schema: 1, scope: 'post_restore_http', passed: false, support_may_open: false,
    public_entry_state: 'operator_attested_not_verified',
    checks: [], cleanup_passed: false, browser_verified: false};
  let stage = 'preflight';
  const check = (name, condition) => {
    stage = name;
    report.checks.push({name, passed: !!condition});
    if (!condition) throw new Error('check_failed');
  };
  try {
    await adapter.preflight();
    for (const who of ['a', 'b', 'staff']) {
      stage = `old_${who}_denied`;
      check(stage, await adapter.oldDenied(who));
    }
    stage = 'fresh_logins';
    for (const who of ['a', 'b', 'staff']) await adapter.login(who);
    const agent = await adapter.staff();
    check('fresh_staff', typeof agent?.id === 'string' && agent.id.length > 0);
    stage = 'customer_sessions';
    let a = await adapter.session('a'), b = await adapter.session('b');
    check('isolated_customers', !!a.conversation_id && !!b.conversation_id && a.conversation_id !== b.conversation_id);
    check('cross_login_denied', await adapter.crossDenied());
    stage = 'history';
    let history = await adapter.history('a');
    if (history.status === 'closed') {
      stage = 'new_consultation';
      await adapter.newConsultation();
      a = await adapter.session('a');
      history = await adapter.history('a');
    }
    // Never silently claim/close an existing human conversation to make tests pass.
    check('available_test_conversation', history.status === 'bot_active');
    const payload = {event_id: `restore-${id}`, content: `Synthetic restore acceptance ${id}`};
    stage = 'send';
    const sent = await adapter.send(payload);
    check('message_identity', typeof sent.message_id === 'string' && sent.message_id.length > 0 && sent.conversation_id === a.conversation_id);
    stage = 'receipt';
    const receipt = await adapter.receipt(payload);
    check('receipt', receipt.accepted === true && receipt.message_id === sent.message_id && receipt.conversation_id === a.conversation_id);
    stage = 'duplicate_send';
    const duplicate = await adapter.send(payload);
    check('duplicate_identity', duplicate.message_id === sent.message_id);
    history = await adapter.history('a');
    check('stored_once', history.messages.filter(m => m.id === sent.message_id).length === 1);
    const other = await adapter.history('b');
    check('history_isolated', other.conversation_id === b.conversation_id && !other.messages.some(m => m.id === sent.message_id || m.content === payload.content));
    stage = 'handoff';
    await adapter.handoff();
    let desk = await adapter.desk('history', a.conversation_id);
    check('waiting_human', desk.status === 'waiting_human');
    stage = 'claim';
    desk = await adapter.desk('claim', a.conversation_id, {revision: desk.revision});
    check('claimed', desk.status === 'human_active');
    const reply = {event_id: `restore-reply-${id}`, content: `Synthetic human reply ${id}`, revision: desk.revision};
    stage = 'reply';
    await adapter.desk('reply', a.conversation_id, reply);
    desk = await adapter.desk('history', a.conversation_id);
    check('reply_visible', desk.messages.filter(m => m.content === reply.content).length === 1);
    stage = 'close';
    desk = await adapter.desk('close', a.conversation_id, {revision: desk.revision});
    check('closed', desk.status === 'closed');
    stage = 'reload_history';
    history = await adapter.history('a');
    const again = await adapter.history('a');
    check('stable_history', history.status === 'closed' && again.status === 'closed' &&
      history.conversation_id === a.conversation_id && again.conversation_id === a.conversation_id &&
      history.messages.some(m => m.content === reply.content) && JSON.stringify(history.messages) === JSON.stringify(again.messages));
    report.passed = true;
  } catch {
    report.failed_stage = stage;
  } finally {
    // Runs even after partial login/setup. Must attempt all owned-resource cleanup.
    try { await adapter.cleanup(); report.cleanup_passed = true; }
    catch { report.passed = false; report.cleanup_failed = true; }
  }
  return report;
}
