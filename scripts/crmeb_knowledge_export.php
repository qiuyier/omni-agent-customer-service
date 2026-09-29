<?php
// CLI only: configuration stays server-side. No CRMEB HTTP/ORM calls or writes.
// Export complete article state, including publication flags; sync filters it.
if (PHP_SAPI !== 'cli') { http_response_code(404); exit(1); }
$opt = getopt('', ['config:', 'source-id:', 'output:']);
$tmp = null;
$stage = 'options';
try {
    foreach (['config', 'source-id', 'output'] as $name) {
        if (empty($opt[$name]) || !is_string($opt[$name])) throw new RuntimeException('missing option');
    }
    if (!preg_match('/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,47}$/D', $opt['source-id'])) throw new RuntimeException('source');
    $stage = 'configuration';
    $raw = @file_get_contents($opt['config']);
    if ($raw === false) throw new RuntimeException('config');
    $ini = @parse_ini_string(str_replace(["\r\n", "\r"], "\n", $raw), true, INI_SCANNER_RAW);
    $cfg = null;
    foreach ($ini ?: [] as $section => $values) {
        if (strtolower($section) === 'database' && is_array($values)) $cfg = array_change_key_case($values, CASE_LOWER);
    }
    foreach (['hostname', 'hostport', 'database', 'username', 'password', 'prefix'] as $key) {
        if (!isset($cfg[$key])) throw new RuntimeException('configuration field');
    }
    if (!preg_match('/^[a-zA-Z0-9_]+$/D', $cfg['database']) ||
        !preg_match('/^[a-zA-Z0-9_]+$/D', $cfg['prefix']) ||
        !preg_match('/^[a-zA-Z0-9._-]+$/D', $cfg['hostname']) ||
        !ctype_digit((string)$cfg['hostport']) || (int)$cfg['hostport'] < 1 || (int)$cfg['hostport'] > 65535) throw new RuntimeException('unsafe target');
    $stage = 'connection';
    $p = new PDO('mysql:host='.$cfg['hostname'].';port='.$cfg['hostport'].';dbname='.$cfg['database'].';charset=utf8mb4', $cfg['username'], $cfg['password'], [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION, PDO::ATTR_TIMEOUT => 5]);
    $prefix = $cfg['prefix'];
    $stage = 'schema';
    $stmt = $p->prepare('SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN (?,?,?)');
    $stmt->execute([$prefix.'article', $prefix.'article_category', $prefix.'article_content']);
    $engines = $stmt->fetchAll(PDO::FETCH_COLUMN);
    if (count($engines) !== 3 || array_filter($engines, fn($x) => $x !== 'InnoDB')) throw new RuntimeException('nontransactional tables');
    $stage = 'read_transaction';
    $p->exec('SET SESSION MAX_EXECUTION_TIME=10000');
    $p->exec('SET TRANSACTION ISOLATION LEVEL REPEATABLE READ');
    $p->exec('START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY');
    // cid is a string in CRMEB; exact binary comparison avoids incompatible
    // column/connection collations without treating '1,2' or '1junk' as ID 1.
    $sql = "SELECT a.id, g.id AS category_id, a.title, c.content, a.status, a.hide, g.status AS category_status, g.hidden AS category_hidden, g.is_del AS category_deleted, a.author, g.intr FROM {$prefix}article a LEFT JOIN {$prefix}article_category g ON BINARY a.cid=BINARY CAST(g.id AS CHAR) LEFT JOIN {$prefix}article_content c ON c.nid=a.id ORDER BY a.id LIMIT 1001";
    $stage = 'read_articles';
    $rows = $p->query($sql)->fetchAll(PDO::FETCH_ASSOC);
    $stage = 'validate_rows';
    if (count($rows) > 1000) throw new RuntimeException('too many records; no partial export');
    $articles = [];
    foreach ($rows as $row) {
        $stage = 'validate_category_content';
        if ($row['category_id'] === null || $row['content'] === null) throw new RuntimeException('missing category or content; export refused');
        $stage = 'validate_fixture_marker';
        $marker = str_starts_with($row['author'], 'omni-fixture-') ? $row['author'] : '';
        if ($marker !== '' && $row['intr'] !== $marker) throw new RuntimeException('fixture category mismatch');
        $item = ['id'=>(int)$row['id'], 'category_id'=>(int)$row['category_id'], 'title'=>$row['title'], 'content'=>$row['content'], 'marker'=>$marker];
        foreach (['status','hide','category_status','category_hidden','category_deleted'] as $key) $item[$key] = (int)$row[$key];
        $articles[] = $item;
    }
    $p->exec('ROLLBACK');
    $stage = 'publish_file';
    $data = json_encode(['schema_version'=>1, 'source_id'=>$opt['source-id'], 'complete'=>true, 'record_count'=>count($articles), 'exported_at'=>gmdate('Y-m-d\TH:i:s\Z'), 'articles'=>$articles], JSON_UNESCAPED_UNICODE|JSON_PRETTY_PRINT|JSON_THROW_ON_ERROR);
    if (strlen($data) > 16*1024*1024) throw new RuntimeException('snapshot size limit');
    $parent = realpath(dirname($opt['output']));
    if ($parent === false || is_link($opt['output'])) throw new RuntimeException('invalid output');
    $tmp = tempnam($parent, '.crmeb-export-');
    if ($tmp === false || !chmod($tmp, 0600)) throw new RuntimeException('temporary file');
    if (file_put_contents($tmp, $data."\n") !== strlen($data)+1 || !rename($tmp, $opt['output'])) throw new RuntimeException('publish');
    $tmp = null;
    echo json_encode(['exported'=>count($articles), 'source_id'=>$opt['source-id']]).PHP_EOL;
} catch (Throwable $e) {
    try { if (isset($p) && $p->inTransaction()) $p->rollBack(); } catch (Throwable $ignored) {}
    if ($tmp !== null && is_file($tmp)) unlink($tmp);
    // Database messages may contain credentials/addresses. Never echo them.
    $code = $e instanceof PDOException && isset($e->errorInfo[1]) ? ' (driver code '.(int)$e->errorInfo[1].')' : '';
    fwrite(STDERR, "CRMEB export failed at ".$stage.$code."; previous snapshot preserved.\n");
    exit(1);
}
