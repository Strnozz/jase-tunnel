import { mkdirSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
mkdirSync('.deploy-check', {recursive:true});
const cli=process.env.npm_execpath;
if(!cli)throw new Error('Run this script through npm run sdk:pack.');
const result=spawnSync(process.execPath,[cli,'pack','./packages/sdk','--pack-destination','.deploy-check'],{stdio:'inherit'});
if(result.error||result.status!==0)throw new Error('SDK packaging failed.');
