import {readFileSync,readdirSync} from 'node:fs';
import {join,relative,sep} from 'node:path';
import {parse} from 'yaml';
import {test} from '@e2e-dev/web';
import {expect} from 'e2e';
const root='../../docs/content/docs';
function pages(directory=root):{route:string;title:string}[]{
 return readdirSync(directory,{withFileTypes:true}).flatMap(entry=>{
  const path=join(directory,entry.name);
  if(entry.isDirectory())return pages(path);
  if(!/\.mdx?$/.test(path))return [];
  const front=readFileSync(path,'utf8').match(/^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/)?.[1];
  const data=front===undefined?null:parse(front);
  if(typeof data?.title!=='string'||!data.title.trim())throw new Error(`Missing title: ${path}`);
  return [{title:data.title,route:'/docs/'+relative(root,path).split(sep).join('/').replace(/\.mdx?$/,'').replace(/(^|\/)index$/,'')}];
 });
}
for(const page of pages())test(`rendered documentation ${page.route}`,async({app,screen,browser})=>{
 await app.open(page.route);
 await expect(screen.getByRole('heading',{name:page.title,exact:true})).toBeVisible();
 const markdown=await browser.evaluate(async path=>{const response=await fetch('/llms.mdx'+path+'/content.md');return {status:response.status,text:await response.text()};},page.route.replace(/\/$/,''));
 expect(markdown.status).toBe(200);
 expect(markdown.text.length).toBeGreaterThan(20);
});
test('mobile docs navigation and readable content',async({app,browser,screen})=>{
 await browser.setViewport({width:390,height:844});
 await app.open('/docs');
 await expect(screen.getByRole('heading',{name:pages().find(p=>p.route==='/docs/')!.title,exact:true})).toBeVisible();
 expect(await browser.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1)).toBe(true);
});
