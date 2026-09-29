const fs=require('fs'),cp=require('child_process'),assert=require('assert/strict'),crypto=require('crypto');
const dir='/tmp/crmeb-fixture-20260928',marker='omni-fixture-20260928-v1';
const docs=[
{key:'pickup',title:'【虚构测试】自提服务说明',keywords:['自提'],body:'仅用于客服接入测试，不代表任何真实商家的政策。测试自提点开放时间为周二至周六10:00—18:00（北京时间）。到店前请等待商家确认可自提。资料未提供具体地址，也未提供查询页面。'},
{key:'booking',title:'【虚构测试】咨询预约指引',keywords:['预约'],body:'仅用于客服接入测试，不代表真实业务功能。测试咨询预约步骤：打开服务工作台，选择咨询预约，选择可用时段，点击确认。完成后页面显示预约编号。这只代表咨询预约成功，不代表订单或售后处理完成。'},
{key:'aftersales',title:'【虚构测试】售后咨询范围',keywords:['售后','退货'],body:'仅用于客服接入测试，不代表真实退货政策。测试售后问题可向商家工作人员咨询。现有资料没有说明退货时限、退货运费承担方、退款到账时间或办理入口。客服当前不能代客户提交申请或执行退款。'}
];
const c={};let section='';for(const l of fs.readFileSync('/Users/LOCAL_USER/Code/DockerProject/dnmp8/www/CRMEB/crmeb/.env','utf8').split(/\r\n|\r|\n/)){const h=l.match(/^\s*\[([^\]]+)\]/);if(h){section=h[1].toLowerCase();continue}const m=l.match(/^\s*(\w+)\s*=\s*(.*?)\s*$/);if(section==='database'&&m)c[m[1].toLowerCase()]=m[2].replace(/^(["'])(.*)\1$/,'$2')}
assert.equal(c.hostname,'mysql');assert.equal(c.database,'crmeb');assert.equal(c.prefix,'eb_');
const mode=process.argv[2];assert(['seed','export'].includes(mode));
if(mode==='seed'){assert(!fs.existsSync(dir));fs.mkdirSync(dir);fs.writeFileSync(dir+'/planned.json',JSON.stringify({marker,docs},null,2));fs.copyFileSync(__filename,dir+'/seed-export.cjs')}
const payload=Buffer.from(JSON.stringify({c,docs,marker,mode})).toString('base64');
const php=`<?php
try {
$x=json_decode(base64_decode('${payload}'),true);$c=$x['c'];$m=$x['marker'];
$p=new PDO('mysql:host='.$c['hostname'].';port='.$c['hostport'].';dbname='.$c['database'].';charset=utf8mb4',$c['username'],$c['password'],[PDO::ATTR_ERRMODE=>PDO::ERRMODE_EXCEPTION]);
$p->exec('SET SESSION MAX_EXECUTION_TIME=5000');
$tables=$p->query("SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('eb_article','eb_article_category','eb_article_content')")->fetchAll(PDO::FETCH_COLUMN);
if(count($tables)!==3||array_filter($tables,fn($v)=>$v!=='InnoDB'))throw new Exception('unsafe_schema');
if($p->query("SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=DATABASE() AND EVENT_OBJECT_TABLE IN ('eb_article','eb_article_category','eb_article_content')")->fetchColumn()!=0)throw new Exception('unexpected_triggers');
if($x['mode']==='seed'){
$p->beginTransaction();$s=$p->prepare('SELECT id FROM eb_article_category WHERE intr=?');$s->execute([$m]);if($s->fetch())throw new Exception('duplicate_fixture');
$p->prepare('INSERT INTO eb_article_category(title,intr,status,hidden,add_time) VALUES(?,?,0,1,?)')->execute(['虚构客服接入测试',$m,(string)time()]);$cid=(int)$p->lastInsertId();$ids=[];
foreach($x['docs'] as $d){$p->prepare('INSERT INTO eb_article(cid,title,author,synopsis,status,hide,add_time) VALUES(?,?,?,?,0,1,?)')->execute([(string)$cid,$d['title'],$m,'虚构数据，仅供接入验收',(string)time()]);$id=(int)$p->lastInsertId();$p->prepare('INSERT INTO eb_article_content(nid,content) VALUES(?,?)')->execute([$id,htmlspecialchars('<p>'.$d['body'].'</p>',ENT_QUOTES,'UTF-8')]);$ids[]=$id;}
$p->commit();echo json_encode(['marker'=>$m,'category_id'=>$cid,'article_ids'=>$ids,'hidden'=>true],JSON_UNESCAPED_UNICODE|JSON_PRETTY_PRINT);
}else{
$p->exec('START TRANSACTION READ ONLY');$s=$p->prepare('SELECT a.id,a.cid,a.title,a.author,a.status,a.hide,a.visit,c.content FROM eb_article a JOIN eb_article_category g ON CAST(a.cid AS UNSIGNED)=g.id JOIN eb_article_content c ON a.id=c.nid WHERE a.author=? AND g.intr=? ORDER BY a.id');$s->execute([$m,$m]);$rows=$s->fetchAll(PDO::FETCH_ASSOC);$p->exec('ROLLBACK');echo json_encode(['marker'=>$m,'rows'=>$rows],JSON_UNESCAPED_UNICODE|JSON_PRETTY_PRINT);
}
}catch(Throwable $e){if(isset($p)&&$p->inTransaction())$p->rollBack();echo json_encode(['error'=>'fixture_operation_failed','code'=>(string)$e->getCode()]);exit(1);}
`;
const r=cp.spawnSync('docker',['exec','-i','php82','php'],{input:php,encoding:'utf8',timeout:20000,maxBuffer:1024*1024});if(r.error||r.status!==0){console.log('Fixture operation failed; credentials and stderr withheld');process.exit(1)}const data=JSON.parse(r.stdout);assert(!data.error);
if(mode==='seed'){fs.writeFileSync(dir+'/manifest.json',JSON.stringify(data,null,2));console.log(JSON.stringify(data));}else{
const manifest=JSON.parse(fs.readFileSync(dir+'/manifest.json'));assert.deepEqual(data.rows.map(r=>Number(r.id)),manifest.article_ids);assert.equal(data.rows.length,docs.length);
const decode=s=>s.replace(/&lt;/g,'<').replace(/&gt;/g,'>').replace(/&quot;/g,'"').replace(/&#039;/g,"'").replace(/&amp;/g,'&');
const faq=data.rows.map((r,i)=>{assert.equal(r.title,docs[i].title);assert.equal(Number(r.status),0);assert.equal(Number(r.hide),1);assert.equal(Number(r.visit),0);const html=decode(r.content);assert(html.startsWith('<p>')&&html.endsWith('</p>'));const text=html.slice(3,-4);assert.equal(text,docs[i].body);return {id:'crmeb-test-'+r.id,title:r.title,source:'crmeb-test/article/'+r.id,content:text,keywords:docs[i].keywords}});
fs.writeFileSync(dir+'/snapshot.json',JSON.stringify({exported_at:new Date().toISOString(),...data},null,2));fs.writeFileSync(dir+'/faq.json',JSON.stringify(faq,null,2));console.log(JSON.stringify({exported:faq.length,ids:faq.map(x=>x.id),sha256:crypto.createHash('sha256').update(JSON.stringify(faq)).digest('hex'),directory:dir}));}
