// Host-owned vocabulary and state labels. The gateway receives only the answer.
const prompt='你想查哪一笔维修？请提供维修单号，例如 R-123。';
const vague=/^(?:请问)?(?:我(?:送修的|的)?(?:设备|手机|电脑)?|这台设备)(?:现在)?(?:修好了吗|修好了没|修到哪了|维修进度怎么样)[？?。]?$/u;
const direct=/^(?:帮我查一下|查一下|请问)?维修单(?:号)?[:：]?\s*(R-[0-9]{1,12})\s*(?:的)?(?:进度|现在修好了吗|修好了吗|修到哪了)[？?。]?$/iu;
const choice=/^(?:维修单(?:号)?[:：]?\s*)?(R-[0-9]{1,12})[。]?$/iu;
const labels=new Map([['inspecting','正在检测'],['repairing','正在维修'],['ready','已修好，可以领取']]);

// Only the host knows this schema. No customer ID is taken from the message.
export function sqliteRepairRead(db){
  const read=db.prepare('SELECT state FROM repairs WHERE customer=? AND ticket=?');
  return (customerID,number)=>read.get(customerID,number);
}

export function repairProgress({lookup}){
  return async ({customerID,content,previousExchange})=>{
    const text=content.trim();
    if(vague.test(text))return {kind:'clarify',text:prompt};
    let match=direct.exec(text);
    if(!match){
      match=choice.exec(text);
      if(!match)return {kind:'pass'};
      const prev=await previousExchange();
      if(prev?.answer!==prompt||!vague.test(prev.question.trim()))return {kind:'pass'};
    }
    try{
      const row=await lookup(customerID,match[1].toUpperCase());
      if(!row)return {kind:'answer',text:'没有找到你可查看的维修单。'};
      const state=labels.get(row.state);
      return state?{kind:'answer',text:'这笔维修单'+state+'。'}:{kind:'unavailable',text:'暂时无法确认这笔维修单的进度。'};
    }catch{return {kind:'unavailable',text:'暂时查不到维修进度，请稍后再试。'};}
  };
}
