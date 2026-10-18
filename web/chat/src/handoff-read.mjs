// Reading does not acknowledge a pending send or permit a second write.
export const canReadDuringSend = (sending,status) =>
 Boolean(sending && ['waiting_human','human_active','closed'].includes(status));
