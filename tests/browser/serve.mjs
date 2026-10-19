import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const require=createRequire(import.meta.url);
const root=fileURLToPath(new URL('../../docs/',import.meta.url));
const child=spawn(process.execPath,[require.resolve('next/dist/bin/next'),'start','--hostname','127.0.0.1','--port','4192'],{cwd:root,stdio:'inherit',env:{...process.env,NEXT_TELEMETRY_DISABLED:'1'}});
for(const signal of ['SIGINT','SIGTERM'])process.on(signal,()=>child.kill(signal));
child.once('error',error=>{console.error(error);process.exitCode=1;});
child.once('exit',code=>{process.exitCode=code??0;});
