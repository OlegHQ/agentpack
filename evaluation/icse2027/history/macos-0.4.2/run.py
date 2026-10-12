#!/usr/bin/env python3
import os,json,time,subprocess,tempfile,hashlib,statistics,platform,shutil,sys,stat,tarfile,io,argparse
from pathlib import Path
parser=argparse.ArgumentParser(description='Real CLI synthetic artifact benchmark. No model inference requested.')
parser.add_argument('--root',type=Path,default=Path(__file__).resolve().parents[3],help='Workspace root for default paths')
parser.add_argument('--agentpack','--binary',dest='binary',type=Path,help='Released Agentpack 0.4.2 binary')
parser.add_argument('--skills',type=Path,help='Built Skills pinned source directory')
parser.add_argument('--baseline-root',type=Path,help='Directory containing built skills/ and ruler/ clones')
parser.add_argument('--control-only',action='store_true',help='Run one independent clean restore/preflight control')
parser.add_argument('--ruler',type=Path,help='Built Ruler pinned source directory')
parser.add_argument('--codex',help='Controlled Codex 0.159.2 executable')
parser.add_argument('--output',type=Path,default=Path(__file__).resolve().parent)
parser.add_argument('--fixtures',type=Path,default=Path(__file__).resolve().parent/'fixtures')
args=parser.parse_args(); args.fixtures=args.fixtures.resolve(); ROOT=args.root.resolve(); HERE=ROOT/'work/icse-submission/measurement'
OUT=args.output.resolve(); AP=(args.binary or ROOT/'work/release-audit/published-v0.4.2/macos-arm64/agentpack').resolve()
baselines=args.baseline_root or HERE
SKILLS=(args.skills or baselines/'skills').resolve(); RULER=(args.ruler or baselines/'ruler').resolve(); NODE=shutil.which('node'); CODEX=args.codex or shutil.which('codex'); ROWS=[]
dependencies=[AP,args.fixtures/'clean.json',args.fixtures/'unknown.json']
if not args.control_only: dependencies += [SKILLS/'bin/cli.mjs',SKILLS/'dist/cli.mjs',RULER/'dist/cli/index.js']
for target in dependencies:
 if not target.exists(): parser.error(f'Missing dependency: {target}. See README.md for pinned setup.')
if not args.control_only and (not NODE or not CODEX): parser.error('node and controlled codex executable are required; see README.md')
if not args.control_only and subprocess.check_output([CODEX,'--version'],text=True).strip()!='codex-cli 0.159.2': parser.error('Controlled Codex 0.159.2 required for this protocol')
if not args.control_only and subprocess.check_output(['git','-C',str(SKILLS),'rev-parse','HEAD'],text=True).strip()!='13e4063a1cf913f5606d57d42ab83a86f5001e04': parser.error('Skills source SHA does not match the pinned benchmark')
if not args.control_only and subprocess.check_output(['git','-C',str(RULER),'rev-parse','HEAD'],text=True).strip()!='0fa2caeef4efaef4c5f8ffbedee8feca4b0b00e2': parser.error('Ruler source SHA does not match the pinned benchmark')
def snap(p):
 result={}
 for f in p.rglob('*'):
  mode=stat.S_IMODE(f.lstat().st_mode)
  value='link:'+os.readlink(f) if f.is_symlink() else hashlib.sha256(f.read_bytes()).hexdigest() if f.is_file() else 'directory'
  result[str(f.relative_to(p))]={'mode':mode,'value':value}
 return result
def run(tool,case,cmd,cwd,env,expected=None):
 before=snap(cwd); start=time.perf_counter(); p=subprocess.run(list(map(str,cmd)),cwd=cwd,env=env,capture_output=True,text=True,timeout=60); elapsed=(time.perf_counter()-start)*1000
 after=snap(cwd); row=dict(tool=tool,case=case,argv=list(map(str,cmd)),exit=p.returncode,elapsed_ms=elapsed,stdout=p.stdout,stderr=p.stderr,checkout_changed=sorted(k for k in set(before)|set(after) if before.get(k)!=after.get(k)),oracle_pass=(expected is None or p.returncode==expected)); ROWS.append(row)
 try:
  parsed=json.loads(p.stdout); row['finding_codes']=[f['code'] for f in parsed.get('findings',[])]; row['contract_results']=[{'id':x['requirement_id'],'result':x['result']} for x in parsed.get('contract_results',[])]
 except (ValueError,TypeError,AttributeError): pass
 if expected is not None and p.returncode!=expected: raise RuntimeError(f'{tool} {case}: expected {expected}, got {p.returncode}')
 return p
with tempfile.TemporaryDirectory(prefix='agentpack-compare-') as td:
 base=Path(td); home=base/'home'; home.mkdir(); env={**os.environ,'HOME':str(home),'USERPROFILE':str(home),'XDG_CONFIG_HOME':str(home/'config'),'XDG_DATA_HOME':str(home/'data'),'XDG_STATE_HOME':str(home/'state'),'XDG_CACHE_HOME':str(home/'cache'),'AGENTPACK_HOME':str(base/'managed'),'AGENTPACK_STAGING_ROOT':str(base/'staging'),'DISABLE_TELEMETRY':'1','DO_NOT_TRACK':'1','CI':'1','CLAUDE_CODE_PATH':str(AP),'CURSOR_AGENT_PATH':str(AP)}
 source=base/'skill'; source.mkdir(); (source/'SKILL.md').write_text('---\nname: held-out-review\ndescription: Independently authored benign review fixture.\n---\n\nCheck the README. Do not invoke tools.\n')
 definition=base/'definition'; ws=base/'workspace'; ws.mkdir(); (ws/'README.md').write_text('Benign project\n')
 run('agentpack','prepare_init',[AP,'env','init','--dir',definition,'--name','comparison'],ws,env,0)
 run('agentpack','prepare_add',[AP,'--env',definition,'--workspace',ws,'add',source,'--no-sync'],ws,env,0)
 if args.control_only:
  run('agentpack','control_restore',[AP,'--env',definition,'--workspace',ws,'env','restore','--strict-external'],ws,env,0)
  result=run('agentpack','control_preflight',[AP,'--env',definition,'--workspace',ws,'preflight','--agent','claude','--strict-external','--contract',args.fixtures/'clean.json','--json'],ws,env,0)
  report=json.loads(result.stdout); assert report['contract_results'][0]['result']=='satisfied' and not ROWS[-1]['checkout_changed']
  print(json.dumps({'control':'clean_external_restore_preflight','exit':0,'contract':'satisfied','checkout_unchanged':True})); sys.exit(0)
 for i in range(7):
  checkout=base/f'ap-{i}'; checkout.mkdir(); (checkout/'README.md').write_text('Benign project\n')
  run('agentpack',f'external_restore_{i}',[AP,'--env',definition,'--workspace',checkout,'env','restore','--strict-external'],checkout,env,0)
  stagehash=hashlib.sha256(str(definition.resolve()).encode()).hexdigest()[:16]
  staged_skill=base/'staging/projects'/stagehash/'modes/default/plugins/agentpack-bundle/skills/skill/SKILL.md'
  assert staged_skill.read_bytes()==(source/'SKILL.md').read_bytes()
  ROWS[-1]['artifact_oracle_pass']=True
  checkout=base/f'skills-{i}'; checkout.mkdir(); (checkout/'README.md').write_text('Benign project\n')
  run('skills',f'skill_install_{i}',[NODE,SKILLS/'bin/cli.mjs','add',source,'--agent','claude-code','--yes'],checkout,env,0)
  assert (checkout/'.claude/skills/held-out-review/SKILL.md').read_bytes()==(source/'SKILL.md').read_bytes()
  ROWS[-1]['artifact_oracle_pass']=True
  checkout=base/f'ruler-{i}'; checkout.mkdir(); (checkout/'README.md').write_text('Benign project\n'); rule=home/'config/ruler'; rule.mkdir(parents=True,exist_ok=True); (rule/'review.md').write_text('Check the README. Do not invoke tools.\n')
  run('ruler',f'external_rule_apply_{i}',[NODE,RULER/'dist/cli/index.js','apply','--project-root',checkout,'--agents','claude','--no-mcp','--no-gitignore','--no-backup'],checkout,env,0)
  assert 'Check the README. Do not invoke tools.' in (checkout/'CLAUDE.md').read_text()
  ROWS[-1]['artifact_oracle_pass']=True
 run('agentpack','prepare_original_restore',[AP,'--env',definition,'--workspace',ws,'env','restore','--strict-external'],ws,env,0)
 # Best-practice alternatives: skills global install and temporary prompt use avoid checkout writes.
 for i in range(7):
  checkout=base/f'skills-global-{i}'; checkout.mkdir(); (checkout/'README.md').write_text('Benign project\n')
  run('skills',f'global_skill_install_{i}',[NODE,SKILLS/'bin/cli.mjs','add',source,'--agent','claude-code','--yes','--global'],checkout,env,0)
  assert (home/'.claude/skills/held-out-review/SKILL.md').read_bytes()==(source/'SKILL.md').read_bytes()
  ROWS[-1]['artifact_oracle_pass']=True
  prompt=run('skills',f'temporary_prompt_use_{i}',[NODE,SKILLS/'bin/cli.mjs','use',source,'--skill','held-out-review'],checkout,env,0)
  assert (source/'SKILL.md').read_text().strip() in prompt.stdout
  ROWS[-1]['artifact_oracle_pass']=True
 # Remove deliberate baseline global skill before independent Agentpack fault controls.
 shutil.rmtree(home/'.claude',ignore_errors=True)
 run('agentpack','prepare_original_restore',[AP,'--env',definition,'--workspace',ws,'env','restore','--strict-external'],ws,env,0)
 # Controlled package-hash baseline: same lock must remain identical across injected non-package faults.
 lock=definition/'pack.lock'; lockhash=hashlib.sha256(lock.read_bytes()).hexdigest()
 fixture=args.fixtures
 def pre(case,args,expected):
  run('agentpack',case,[AP,'--env',definition,'--workspace',ws,'preflight','--json',*args],ws,env,expected)
  ROWS.append(dict(tool='package_hash_control',case=case,lock_changed=hashlib.sha256(lock.read_bytes()).hexdigest()!=lockhash,detected=False,applicable=True))
 pre('clean_no_checkout_write',['--agent','claude','--strict-external','--contract',fixture/'clean.json'],0)
 pre('missing_native_receipt',['--agent','claude','--strict-external','--contract',fixture/'unknown.json'],4)
 pre('unsupported_workspace_overlay',['--agent','cursor','--strict-external','--contract',fixture/'clean.json'],3)
 settings=home/'.claude/settings.json'; settings.parent.mkdir(parents=True); settings.write_text('{"env":{"BENIGN_HELD_OUT":"1"}}')
 pre('inherited_user_config',['--agent','claude','--strict-external','--policy','ci'],3); settings.unlink()
 contract=base/'named.json'; contract.write_text(json.dumps({'schema_version':1,'unknown_required':'fail','requirements':[{'id':'named','selector':{'category':'skill','name':'not-the-installed-skill'},'predicate':'present','minimum_evidence':'generated'}]}))
 pre('named_artifact_mismatch',['--agent','claude','--contract',contract],3)
 receipt=base/'receipt.json'; receipt.write_text('{"schema_version":1,"coverage":[{"category":"skill","scope":"native_catalog","completeness":"complete"}]}')
 pre('malformed_complete_receipt',['--agent','claude','--receipt',receipt,'--contract',fixture/'unknown.json'],5)
 # External relocation identity and pure preflight checks, independently verified.
 identities=[]
 for i in range(7):
  checkout=base/f'pre-{i}'; checkout.mkdir(); (checkout/'README.md').write_text('Benign project\n')
  run('agentpack',f'prepare_preflight_{i}',[AP,'--env',definition,'--workspace',checkout,'env','restore','--strict-external'],checkout,env,0)
  p=run('agentpack',f'pure_preflight_{i}',[AP,'--env',definition,'--workspace',checkout,'preflight','--agent','claude','--strict-external','--contract',fixture/'clean.json','--json'],checkout,env,0); identities.append(json.loads(p.stdout)['source_identity'])
 assert len(set(identities))==1
 # Native wrapper receives own disposable home and copies only benign skill; no accounts/config/hooks copied.
 codex=CODEX; nh=base/'native'; nh.mkdir(); ch=nh/'codex'; (ch/'skills/held-out-review').mkdir(parents=True); shutil.copyfile(source/'SKILL.md',ch/'skills/held-out-review/SKILL.md')
 ne={**env,'HOME':str(nh),'USERPROFILE':str(nh),'CODEX_HOME':str(ch),'XDG_CONFIG_HOME':str(nh/'config')}
 for i in range(7):
  p=run('native_wrapper',f'positive_skill_discovery_{i}',[codex,'-c','features.cloud_skills=false','-c','cli_auth_credentials_store="file"','--no-daemon','debug','prompt-input','benign metadata probe'],ws,ne,0)
  ROWS[-1]['sentinel_observed']='held-out-review' in p.stdout
  assert ROWS[-1]['sentinel_observed']
 # Live Agentpack receipt control, then independently injected ambient edit and diagnostic loss.
 env['CODEX_PATH']=codex
 observed_contract=definition/'contract.json'; observed_contract.write_text(json.dumps({'schema_version':1,'unknown_required':'fail','requirements':[{'id':'controlled-skill','selector':{'category':'skill','name':'held-out-review','scope':'controlled_projection'},'predicate':'present','minimum_evidence':'observed'}]}))
 run('agentpack','prepare_receipt_restore',[AP,'--env',definition,'--workspace',ws,'env','restore','--strict-external'],ws,env,0)
 p=run('agentpack','native_receipt_clean',[AP,'--env',definition,'--workspace',ws,'probe','--agent','codex','--strict-external','--json'],ws,env,0)
 receipt_obj=json.loads(p.stdout)
 pre('valid_native_receipt_clean',['--agent','codex','--strict-external','--receipt','latest'],0)
 ambient=ws/'.agents/skills/ambient-held-out/SKILL.md'; ambient.parent.mkdir(parents=True); ambient.write_text('---\nname: ambient-held-out\ndescription: benign ambient edit\n---\nNothing.\n')
 pre('stale_receipt_workspace_edit',['--agent','codex','--strict-external','--receipt','latest'],4)
 shutil.rmtree(ws/'.agents')
 pre('restored_receipt_clean',['--agent','codex','--strict-external','--receipt','latest'],0)
 diagnostic=next((q.get('evidence_ref') for q in receipt_obj['properties'] if q.get('evidence_ref')),None)
 assert diagnostic
 pre('observed_contract_clean',['--agent','codex','--strict-external','--receipt','latest','--contract',observed_contract],0)
 Path(diagnostic).unlink()
 pre('missing_diagnostic_evidence',['--agent','codex','--strict-external','--receipt','latest','--contract',observed_contract],5)
 # Bundle controls are separate from configuration fault classification.
 local_bundle=base/'local.bundle'; refused=run('agentpack','local_payload_export_refused',[AP,'--env',definition,'env','export',local_bundle],ws,env)
 assert refused.returncode!=0 and not local_bundle.exists(); ROWS[-1]['artifact_oracle_pass']=True
 empty=base/'empty-definition'; run('agentpack','prepare_bundle_init',[AP,'env','init','--dir',empty,'--name','empty-bundle'],ws,env,0)
 bundle=base/'empty.bundle'; run('agentpack','safe_bundle_export',[AP,'--env',empty,'env','export',bundle],ws,env,0)
 imported=base/'imported'; run('agentpack','safe_bundle_import',[AP,'env','import',bundle,'--dir',imported],ws,env,0)
 assert (empty/'pack.lock').read_bytes()==(imported/'pack.lock').read_bytes(); ROWS[-1]['artifact_oracle_pass']=True
 malicious=base/'malicious.bundle'
 with tarfile.open(malicious,'w:gz') as archive:
  info=tarfile.TarInfo('../escaped'); info.size=4; archive.addfile(info,io.BytesIO(b'evil'))
 rejected=base/'rejected'; rejected_result=run('agentpack','traversal_bundle_refused',[AP,'env','import',malicious,'--dir',rejected],ws,env)
 assert rejected_result.returncode!=0 and not rejected.exists() and not (base/'escaped').exists(); ROWS[-1]['artifact_oracle_pass']=True
 summary=[]
 for tool,prefix in [('agentpack','external_restore_'),('skills','skill_install_'),('ruler','external_rule_apply_'),('skills','global_skill_install_'),('skills','temporary_prompt_use_'),('agentpack','pure_preflight_'),('native_wrapper','positive_skill_discovery_')]:
  rr=[r for r in ROWS if r.get('tool')==tool and r.get('case','').startswith(prefix)]; ts=[r['elapsed_ms'] for r in rr]; q=statistics.quantiles(ts,n=4,method='inclusive')
  summary.append(dict(tool=tool,task=prefix.rstrip('_'),n=len(ts),median_ms=statistics.median(ts),q1_ms=q[0],q3_ms=q[2],successes=sum(r['oracle_pass'] for r in rr),checkout_writes_runs=sum(bool(r['checkout_changed']) for r in rr)))
 metadata={'date':'2026-10-11','platform':platform.platform(),'machine':platform.machine(),'node':subprocess.check_output([NODE,'--version'],text=True).strip(),'agentpack_sha256':hashlib.sha256(AP.read_bytes()).hexdigest(),'skills_sha':subprocess.check_output(['git','-C',str(SKILLS),'rev-parse','HEAD'],text=True).strip(),'ruler_sha':subprocess.check_output(['git','-C',str(RULER),'rev-parse','HEAD'],text=True).strip(),'native_version':subprocess.check_output([codex,'--version'],text=True).strip(),'source_identity_equal_across_7_relocations':True,'summary':summary,'rows':ROWS}
 OUT.mkdir(parents=True,exist_ok=True); HERE.mkdir(parents=True,exist_ok=True); private=HERE/'private-raw-results.json'; private.write_text(json.dumps(metadata,indent=2)); private.chmod(0o600)
 for row in metadata['rows']:
  for field in ['stdout','stderr']:
   data=row.pop(field,''); row[field+'_sha256']=hashlib.sha256(data.encode()).hexdigest(); row[field+'_bytes']=len(data.encode())
  if 'argv' in row: row['argv']=[x.replace(str(base),'<FIXTURE_ROOT>').replace(str(ROOT),'<WORKSPACE_ROOT>').replace(str(CODEX),'<CODEX_EXECUTABLE>').replace(str(NODE),'<NODE_EXECUTABLE>') for x in row['argv']]
 (OUT/'comparative-results.json').write_text(json.dumps(metadata,indent=2)); print(json.dumps(summary,indent=2))
