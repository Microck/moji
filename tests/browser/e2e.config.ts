import type {E2EConfig} from 'e2e';
import {web} from '@e2e-dev/web';
export default {tests:'*.e2e.ts',workers:1,retries:0,trace:'on',timeout:30000,targets:[{name:'chromium',engine:web(),app:{url:'http://127.0.0.1:4192',command:{executable:'node',args:['serve.mjs'],log:'.e2e/server.log'}}}]} satisfies E2EConfig;
