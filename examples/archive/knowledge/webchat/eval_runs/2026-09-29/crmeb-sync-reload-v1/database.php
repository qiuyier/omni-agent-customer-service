<?php
// $task is supplied by the local harness, never by an HTTP request.
function snapshot(PDO $p): array {
    $category=$p->query('SELECT * FROM eb_article_category WHERE id=1 FOR UPDATE')->fetch(PDO::FETCH_ASSOC);
    $articles=$p->query('SELECT * FROM eb_article WHERE id IN (1,2,3) ORDER BY id FOR UPDATE')->fetchAll(PDO::FETCH_ASSOC);
    $contents=$p->query('SELECT * FROM eb_article_content WHERE nid IN (1,2,3) ORDER BY nid FOR UPDATE')->fetchAll(PDO::FETCH_ASSOC);
    if (!$category || $category['intr']!=='omni-fixture-20260928-v1' || count($articles)!==3 || count($contents)!==3) throw new Exception('scope');
    foreach($articles as $a) if($a['author']!=='omni-fixture-20260928-v1'||$a['cid']!=='1'||(int)$a['hide']!==1||(int)$a['status']!==0) throw new Exception('scope');
    if((int)$category['hidden']!==1||(int)$category['status']!==0)throw new Exception('category_scope');
    if((int)$p->query("SELECT COUNT(*) FROM eb_article WHERE cid='1'")->fetchColumn()!==3)throw new Exception('category_not_exclusive');
    return ['category'=>$category,'articles'=>$articles,'contents'=>$contents];
}
try {
    $ini=parse_ini_string(str_replace(["\r\n","\r"],"\n",file_get_contents('/www/CRMEB/crmeb/.env')),true,INI_SCANNER_RAW);$c=null;
    foreach($ini as $s=>$v)if(strtolower($s)==='database')$c=array_change_key_case($v,CASE_LOWER);
    if($c['hostname']!=='mysql'||$c['database']!=='crmeb'||$c['prefix']!=='eb_')throw new Exception('target');
    $p=new PDO('mysql:host=mysql;port='.$c['hostport'].';dbname=crmeb;charset=utf8mb4',$c['username'],$c['password'],[PDO::ATTR_ERRMODE=>PDO::ERRMODE_EXCEPTION]);
    $p->exec('SET SESSION innodb_lock_wait_timeout=5');$p->beginTransaction();
    $before=snapshot($p);$hash=hash('sha256',json_encode($before));
    if($task['mode']!=='snapshot'&&$hash!==$task['expected'])throw new Exception('concurrent_change');
    switch($task['mode']){
    case 'snapshot':break;
    case 'update':
        $text=$before['contents'][0]['content'];if(substr_count($text,'18:00')!==1)throw new Exception('unexpected_text');
        $p->prepare('UPDATE eb_article_content SET content=? WHERE nid=1')->execute([str_replace('18:00','19:00',$text)]);break;
    case 'withdraw':$p->exec('UPDATE eb_article_category SET is_del=1 WHERE id=1');break;
    case 'restore':
        $base=$task['baseline'];
        $p->prepare('UPDATE eb_article_content SET content=? WHERE nid=1')->execute([$base['contents'][0]['content']]);
        $p->prepare('UPDATE eb_article_category SET is_del=? WHERE id=1')->execute([$base['category']['is_del']]);break;
    default:throw new Exception('unknown_mode');
    }
    $after=snapshot($p);$p->commit();echo json_encode(['mode'=>$task['mode'],'state'=>$after,'hash'=>hash('sha256',json_encode($after))],JSON_UNESCAPED_UNICODE|JSON_PRETTY_PRINT);
}catch(Throwable $e){try{if(isset($p)&&$p->inTransaction())$p->rollBack();}catch(Throwable $ignored){}echo json_encode(['error'=>'scoped_database_operation_failed']);exit(1);}
