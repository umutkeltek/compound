import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import {spawn, spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';

const hash = file => crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
export function verifyBundle(bundle, commit, platform) {
  assert(['darwin_arm64', 'linux_amd64'].includes(platform), 'unsupported native platform');
  assert(/^[a-f0-9]{40}$/.test(commit), 'full candidate commit required');
  const manifest = fs.readFileSync(path.join(bundle, 'RELEASE'), 'utf8');
  const version = manifest.match(/^version ((?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*))$/m)?.[1];
  assert(version, 'invalid release version');
  const names = ['RELEASE', 'install-release.sh', ...['darwin_arm64', 'linux_amd64'].flatMap(p => [
    `compound_${version}_${p}.tar.gz`, `compound_${version}_${p}.cdx.json`,
    `compound_${version}_${p}_compound-breadth-engine.cdx.json`,
  ])].sort();
  assert.deepEqual(fs.readdirSync(bundle).sort(), [...names, 'SHA256SUMS'].sort(), 'release inventory');
  for (const name of [...names, 'SHA256SUMS']) assert(fs.lstatSync(path.join(bundle, name)).isFile(), 'regular release files required');
  const checks = fs.readFileSync(path.join(bundle, 'SHA256SUMS'), 'utf8').trimEnd().split('\n').map(line => {
    const match = line.match(/^([a-f0-9]{64})  ([a-zA-Z0-9_.-]+)$/);
    assert(match, 'invalid checksum entry');
    return {name: match[2], sha256: match[1]};
  });
  assert.deepEqual(checks.map(c => c.name).sort(), names, 'checksum inventory');
  for (const c of checks) assert.equal(hash(path.join(bundle, c.name)), c.sha256, 'release checksum mismatch');
  const expected = `schema compound.release.v0.2\nversion ${version}\ncommit ${commit}\ninstaller install-release.sh\n` +
    ['darwin arm64', 'linux amd64'].map(p => {
      const tag = p.replace(' ', '_');
      return `artifact ${p} compound_${version}_${tag}.tar.gz compound_${version}_${tag}.cdx.json compound_${version}_${tag}_compound-breadth-engine.cdx.json\n`;
    }).join('');
  assert.equal(manifest, expected, 'release candidate manifest mismatch');
  return {commit, version, platform, artifacts: [...names, 'SHA256SUMS'].sort().map(name => ({name, path: path.join(bundle, name), sha256: hash(path.join(bundle, name))}))};
}

// This is an acceptance consumer of shipped files, not another execution or evidence owner.
export async function acceptInstalled(bundle, commit, root, priorBundle, priorCommit) {
  assert(path.isAbsolute(root) && !fs.existsSync(root), 'acceptance location must be absolute and absent');
  const platform = `${process.platform}_${process.arch === 'x64' ? 'amd64' : process.arch}`;
  const candidate = verifyBundle(bundle, commit, platform);
  const prior = verifyBundle(priorBundle, priorCommit, platform);
  assert.notEqual(commit, priorCommit, 'prior and candidate generations must differ');
  fs.mkdirSync(root, {mode: 0o700});
  const logs = path.join(root, 'logs');
  fs.mkdirSync(logs, {mode: 0o700});
  const home = path.join(root, 'home'), bins = path.join(root, 'bin'), bin = path.join(bins, 'compound');
  const report = {schema_version: 'compound.release_installed_acceptance.v0.1', candidate: commit,
    status: 'running', started_at: new Date().toISOString(), native: {platform: process.platform, arch: process.arch, kernel: os.release()},
    test_runner_sha256: hash(fileURLToPath(import.meta.url)), artifacts: candidate.artifacts, prior_artifacts: prior.artifacts, steps: [], checks: [], external_effects: [],
    limitations: ['One trusted local OS user; no same-user containment or independently authenticated operator labels.',
      'No provider, scheduler, external account, live/private-corpus promotion, publication or authority-minting exercise.']};
  const deadline = Date.now() + 20 * 60 * 1000;
  // Preserve the trusted OS user's real home for Go's user-profile lookup;
  // Compound state always uses the explicit isolated COMPOUND_HOME below.
  const env = where => ({PATH: '/usr/bin:/bin:/usr/sbin:/sbin', LANG: 'C', HOME: os.homedir(), COMPOUND_HOME: where});
  function record(name, result) {
    const files = {};
    for (const [suffix, body] of [['stdout', result.stdout], ['stderr', result.stderr]]) {
      const file = path.join(logs, `${report.steps.length}-${name}.${suffix}`);
      fs.writeFileSync(file, body ?? '', {flag: 'wx', mode: 0o600});
      files[suffix] = {path: file, sha256: hash(file)};
    }
    report.steps.push({name, exit_code: result.status, signal: result.signal, error: result.error?.message, ...files});
  }
  function run(name, executable, args, where = home, expected = 0, extra = {}) {
    assert(Date.now() < deadline, 'bounded acceptance deadline');
    const result = spawnSync(executable, args, {cwd: root, env: {...env(where), ...extra}, encoding: 'utf8',
      timeout: Math.min(120000, deadline - Date.now()), maxBuffer: 16 * 1024 * 1024});
    record(name, result);
    assert(!result.error, `${name}: ${result.error?.message}`);
    if (expected === 'SIGKILL') {
      assert.equal(result.status, null, `${name}: crash must not report an exit`);
      assert.equal(result.signal, 'SIGKILL', `${name}: exact injected crash required`);
    } else {
      assert(Number.isInteger(result.status), `${name}: unresolved process termination`);
      if (expected === 'nonzero') assert.notEqual(result.status, 0, `${name}: must refuse`);
      else assert.equal(result.status, expected, `${name}: ${result.stderr}`);
    }
    return result;
  }
  const cli = (name, args, where = home, expected = 0) => run(name, bin, args, where, expected);
  const json = (name, args, where = home) => JSON.parse(cli(name, args, where).stdout);
  const pair = dir => Object.fromEntries(['compound', 'compound-breadth-engine'].map(name => [name, hash(path.join(dir, name))]));
  function install(name, selected, dir, where, extra = {}, expected = 0) {
    const v = selected === bundle ? candidate.version : prior.version;
    return run(name, '/bin/sh', [path.join(selected, 'install-release.sh')], where, expected,
      {GOBIN: dir, COMPOUND_VERSION: v, COMPOUND_RELEASE_TARBALL: path.join(selected, `compound_${v}_${platform}.tar.gz`), ...extra});
  }
  const actor = 'codex:installed-release';
  function create(id, goal, where = home) {
    cli(`${id}-create`, ['work', 'create', '--work-id', id, '--state', 'ready', '--owner', actor, '--by', actor,
      '--outcome', goal, '--predicate', 'Independent exact result acceptance'], where);
    cli(`${id}-claim`, ['work', 'claim', id, '--by', actor], where);
  }
  const attemptFile = (where, id) => path.join(where, 'run_attempts', `${id}.json`);
  const workFile = (where, id) => path.join(where, 'work', 'work', `${id}.json`);
  const read = file => JSON.parse(fs.readFileSync(file, 'utf8'));
  const output = attempt => fs.readFileSync(attempt.transcript_ref, 'utf8').trimEnd().split('\n').map(line => {
    assert(line.startsWith('stdout: '), 'unexpected worker output'); return line.slice(8);
  }).join('\n');
  const execute = (id, code, args = [], resume = false) => ['work', 'advance', id, '--to', 'running', '--by', actor,
    '--execute', ...(resume ? ['--resume-verifying'] : []), '--harness', 'os-harness-entrypoints', '--workspace', root,
    '--timeout', '90s', '--', process.execPath, '-e', code, ...args];
  function close(id, where = home, proofRef = '') {
    const result = cli(`${id}-close`, ['work', 'close', id, '--outcome', 'completed', '--move-type', 'audit_only', '--by', actor,
      '--summary', 'Independent exact installed worker result accepted'], where);
    const chain = JSON.parse(result.stdout.slice(result.stdout.indexOf('{')));
    assert.equal(chain.work.state, 'completed'); return chain;
  }
  async function until(predicate, description, childState) {
    const end = Date.now() + 45000;
    while (!predicate()) {
      assert(!childState?.done, `${description}: worker exited early`);
      assert(Date.now() < end, `${description}: bounded readiness timeout`);
      await new Promise(resolve => setTimeout(resolve, 50));
    }
  }
  try {
    install('prior-install', priorBundle, bins, home);
    const oldPair = pair(bins), oldManifest = hash(path.join(home, 'manifest.yaml'));
    assert.equal(JSON.parse(run('prior-version', bin, ['version', '--json']).stdout).build_sha, priorCommit);
    install('upgrade-first-rename-failure', bundle, bins, home, {COMPOUND_INSTALL_FAIL_AFTER_FIRST_RENAME: '1'}, 'nonzero');
    assert.deepEqual(pair(bins), oldPair); assert.equal(hash(path.join(home, 'manifest.yaml')), oldManifest);
    report.checks.push({gate: 'upgrade_failure', passed: true, exact_prior_pair_restored: oldPair});
    install('candidate-install', bundle, bins, home);
    const version = json('installed-version', ['version', '--json']);
    assert.equal(version.build_sha, commit); assert.equal(version.version, candidate.version);
    assert.equal(json('first-use', ['home', 'doctor', '--json']).verdict, 'ok');
    for (const name of ['compound', 'compound-breadth-engine']) assert(fs.statSync(path.join(bins, name)).mode & 0o111);
    report.installed = {version, absolute_binary: bin, pair: pair(bins)};
    report.checks.push({gate: 'installation_first_use', passed: true, explicit_home: home});

    const sweepRefusal = cli('unsupported-sweep-disabled', ['work', 'sweep-stale', '--yes'], home, 'nonzero');
    assert((sweepRefusal.stdout + sweepRefusal.stderr).includes('--experimental-sweep-stale-mutations'));
    const postgresRefusal = run('unsupported-postgres-disabled', bin, ['store', 'status', '--json'],
      home, 'nonzero', {COMPOUND_STORE: 'postgres'});
    assert((postgresRefusal.stdout + postgresRefusal.stderr).includes('COMPOUND_EXPERIMENTAL_POSTGRES=1'));
    assert.equal(hash(path.join(home, 'manifest.yaml')), oldManifest);
    report.checks.push({gate: 'excluded_defaults', passed: true, sweep_mutation_disabled: true,
      partial_postgres_selection_refused: true, home_manifest_unchanged: true});

    const recoveryBins = path.join(root, 'recovery-bin'), recoveryHome = path.join(root, 'recovery-home');
    install('recovery-prior-install', priorBundle, recoveryBins, recoveryHome);
    const failure = install('rollback-restoration-failure', bundle, recoveryBins, recoveryHome,
      {COMPOUND_INSTALL_FAIL_AFTER_FIRST_RENAME: '1', COMPOUND_INSTALL_FAIL_RESTORE_COMPOUND: '1'}, 'nonzero');
    assert(failure.stderr.includes('recovery transaction preserved'));
    const transaction = path.join(recoveryBins, '.compound-install-state', 'transaction');
    const journal = hash(path.join(transaction, 'journal'));
    assert.equal(hash(path.join(transaction, 'compound.previous')), oldPair.compound);
    const recovered = install('restart-recovers-prior-pair', bundle, recoveryBins, recoveryHome,
      {COMPOUND_INSTALL_CRASH_AT: 'after_recovery'}, 'SIGKILL');
    assert((recovered.stdout + recovered.stderr).includes('recovered interrupted install state'));
    assert.deepEqual(pair(recoveryBins), oldPair);
    install('finish-recovered-upgrade', bundle, recoveryBins, recoveryHome);
    assert.deepEqual(pair(recoveryBins), report.installed.pair);
    report.checks.push({gate: 'rollback_recovery', passed: true, retained_journal_sha256: journal});
    const reverse = 'work_installed_reverse_census', ready = path.join(root, 'reverse-ready'),
      release = path.join(root, 'reverse-release');
    create(reverse, 'Preserve every real launch and newer work authority before independently accepting history.');
    const reverseWorker = "const fs=require('fs');const [ready,release]=process.argv.slice(1);fs.writeFileSync(ready+'.ready','running');fs.renameSync(ready+'.ready',ready);const timer=setInterval(()=>{if(fs.existsSync(release)){clearInterval(timer);console.log('HISTORICAL_ACCEPTED');}},50);";
    const live = spawn(bin, execute(reverse, reverseWorker, [ready, release]), {cwd: root, env: env(home), stdio: ['ignore', 'pipe', 'pipe']});
    const liveState = {done: false}; let liveOut = '', liveErr = '';
    live.stdout.on('data', b => { liveOut += b; }); live.stderr.on('data', b => { liveErr += b; });
    const liveJoin = new Promise((resolve, reject) => {
      live.once('error', reject); live.once('exit', (code, signal) => {
        liveState.done = true; record('reverse-real-launch', {stdout: liveOut, stderr: liveErr, status: code, signal}); resolve(code);
      });
    });
    try {
      await until(() => fs.existsSync(ready), 'real reverse-only launch readiness', liveState);
      const running = json('reverse-running', ['work', 'show', reverse, '--json']);
      assert.equal(running.state, 'running');
      const actual = json('reverse-native-attempt', ['run-attempt', 'list', '--json'])
        .find(a => a.work_id === reverse && a.origin === 'harness_execution');
      assert(actual?.launch?.committed_at); assert.equal(actual.status, 'running');
      assert(!running.run_attempt_ids.includes(actual.id));
      cli('reverse-newer-authority', ['work', 'advance', reverse, '--to', 'verifying', '--by', actor,
        '--note', 'A newer generation must survive finalization of the older real launch.']);
      const newer = json('reverse-newer-work', ['work', 'show', reverse, '--json']), newerHash = hash(workFile(home, reverse));
      assert.notEqual(newer.policy_decision_id, running.policy_decision_id);
      assert(!newer.run_attempt_ids.includes(actual.id));
      const refused = cli('reverse-active-close-refused', ['work', 'close', reverse, '--outcome', 'completed',
        '--move-type', 'audit_only', '--by', actor, '--summary', 'Active reverse-only execution must not certify'], home, 'nonzero');
      assert((refused.stdout + refused.stderr).includes(actual.id));
      assert.equal(hash(workFile(home, reverse)), newerHash);
      fs.writeFileSync(release, 'release owned worker', {flag: 'wx'});
      assert.notEqual(await liveJoin, 0);
      assert(liveErr.includes('remains reverse-linked only'));
      assert(liveErr.includes('preserving its state, policy, and action identity'));
      assert.equal(hash(workFile(home, reverse)), newerHash, 'older launch must not overwrite newer authority');
      const historical = read(attemptFile(home, actual.id)), historyHash = hash(attemptFile(home, actual.id));
      assert.equal(historical.status, 'succeeded'); assert.equal(output(historical), 'HISTORICAL_ACCEPTED');
      const chain = close(reverse, home, 'run-attempt:' + actual.id);
      assert(chain.work.run_attempt_ids.includes(actual.id));
      assert.equal(hash(attemptFile(home, actual.id)), historyHash);
      report.checks.push({gate: 'complete_attempt_accounting', passed: true, attempt_id: actual.id,
        active_reverse_only_close_refused: true, newer_authority_preserved: true, historical_attempt_unchanged: true,
        receipt_id: chain.receipt.id});
    } finally { if (!liveState.done) { live.kill('SIGTERM'); await liveJoin; } }

    const refusal = 'work_installed_prestart_refusal';
    create(refusal, 'Record known refusal before process start without inventing side effects.');
    const refusalArgs = execute(refusal, '');
    refusalArgs.splice(refusalArgs.indexOf('--') + 1, 3, path.join(root, 'absent-executable.invalid'));
    cli('prestart-known-refusal', refusalArgs, home, 'nonzero');
    const refusedWork = json('prestart-failed-work', ['work', 'show', refusal, '--json']);
    assert.equal(refusedWork.state, 'failed');
    const refusedAttempt = json('prestart-launch-evidence', ['run-attempt', 'list', '--json'])
      .find(a => a.id === refusedWork.run_attempt_ids.at(-1));
    assert.equal(refusedAttempt.status, 'failed');
    assert(refusedAttempt.launch?.start_error_at); assert(!refusedAttempt.launch?.committed_at);
    report.checks.push({gate: 'known_prestart_refusal', passed: true, attempt_id: refusedAttempt.id,
      process_not_started: true});

    const rejected = 'work_installed_rejected', goal = 'Return the exact literal ACCEPTED; execution success is not acceptance.';
    create(rejected, goal);
    const worker = "const cp=require('child_process'),a=require('assert/strict');const [bin,id]=process.argv.slice(1);const r=cp.spawnSync(bin,['work','show',id,'--json'],{env:process.env,encoding:'utf8'});a.equal(r.status,0);const w=JSON.parse(r.stdout);a.equal(w.state,'running');a.equal(w.desired_outcome,'Return the exact literal ACCEPTED; execution success is not acceptance.');console.log('REJECTED');";
    cli('rejected-worker', execute(rejected, worker, [bin, rejected]));
    const rejectedWork = json('rejected-verifying', ['work', 'show', rejected, '--json']);
    const rejectedID = rejectedWork.run_attempt_ids.at(-1), rejectedAttempt = read(attemptFile(home, rejectedID));
    assert.equal(rejectedAttempt.status, 'succeeded'); assert.equal(rejectedAttempt.harness, 'os-harness-entrypoints');
    assert.equal(rejectedAttempt.workspace, root); assert.equal(output(rejectedAttempt), 'REJECTED');
    const attemptHash = hash(attemptFile(home, rejectedID)), workHash = hash(workFile(home, rejected));
    cli('caller-marker-does-not-authorize', ['work', 'close', rejected, '--outcome', 'failed', '--move-type', 'audit_only',
      '--by', actor, '--summary', 'Caller marker is not acceptance authority', '--proof-ref', `verification_rejected:${rejectedID}:forged`], home, 'nonzero');
    assert.equal(hash(workFile(home, rejected)), workHash); assert.equal(hash(attemptFile(home, rejectedID)), attemptHash);
    const failed = cli('legitimate-rejection', ['work', 'close', rejected, '--outcome', 'failed', '--move-type', 'audit_only',
      '--by', actor, '--summary', 'Exact literal acceptance failed', '--verification-rejected', rejectedID, '--reason', 'Exact literal acceptance failed']);
    const rejectionChain = JSON.parse(failed.stdout.slice(failed.stdout.indexOf('{')));
    assert.equal(rejectionChain.receipt.outcome, 'failed'); assert.equal(hash(attemptFile(home, rejectedID)), attemptHash);
    assert.equal(json('rejection-proof-doctor', ['work', 'proof-chain-doctor', '--json']).status, 'ok');
    report.checks.push({gate: 'execution_and_rejected_verification', passed: true, work_id: rejected, attempt_id: rejectedID,
      attempt_unchanged: true, caller_marker_refused: true, receipt_id: rejectionChain.receipt.id});

    const continuation = 'work_installed_continuity', partial = path.join(root, 'partial.json');
    create(continuation, 'Continue this saved goal after interruption without rebuilding its brief.');
    const continuationWorker = "const fs=require('fs'),cp=require('child_process'),a=require('assert/strict');const [bin,id,partial,phase]=process.argv.slice(1);const r=cp.spawnSync(bin,['work','show',id,'--json'],{env:process.env,encoding:'utf8'});a.equal(r.status,0);const w=JSON.parse(r.stdout);a.equal(w.state,'running');if(phase==='interrupt'){const c=cp.spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{stdio:'ignore'});c.once('spawn',()=>{fs.writeFileSync(partial+'.ready',JSON.stringify({id,goal:w.desired_outcome,pid:process.pid,child_pid:c.pid}));fs.renameSync(partial+'.ready',partial);});setInterval(()=>{},1000);}else{const p=JSON.parse(fs.readFileSync(partial));a.equal(p.id,id);a.equal(p.goal,w.desired_outcome);console.log(JSON.stringify({accepted:true,id,goal:w.desired_outcome}));}";
    const args = execute(continuation, continuationWorker, [bin, continuation, partial, 'interrupt']);
    args.splice(args.indexOf('--'), 0, '--keep-verifying-on-failure');
    const child = spawn(bin, args, {cwd: root, env: env(home), stdio: ['ignore', 'pipe', 'pipe']});
    const state = {done: false}; let stdout = '', stderr = '';
    child.stdout.on('data', bytes => { stdout += bytes; }); child.stderr.on('data', bytes => { stderr += bytes; });
    const joined = new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', (code, signal) => {
      state.done = true; record('owned-cancellation', {status: code, signal, stdout, stderr}); resolve(code);
    }); });
    try {
      await until(() => fs.existsSync(partial), 'durable owned worker readiness', state);
      const owned = read(partial), partialHash = hash(partial);
      assert(child.kill('SIGINT')); assert.notEqual(await joined, 0);
      const after = json('cancelled-work', ['work', 'show', continuation, '--json']);
      assert.equal(after.state, 'verifying');
      const cancelledID = after.run_attempt_ids.at(-1); assert.equal(read(attemptFile(home, cancelledID)).status, 'failed');
      await until(() => !run(`owned-tree-${report.steps.length}`, '/bin/ps', ['-axo', 'pid=,pgid=,stat=']).stdout.trim().split('\n')
        .map(line => line.trim().match(/^(\d+)\s+(\d+)\s+(\S+)$/)).filter(Boolean)
        .some(m => [owned.pid, owned.child_pid].includes(Number(m[1])) && !m[3].startsWith('Z')), 'owned process tree termination');
      cli('fresh-process-resume', execute(continuation, continuationWorker, [bin, continuation, partial, 'resume'], true));
      const resumed = json('resumed-work', ['work', 'show', continuation, '--json']), resumedID = resumed.run_attempt_ids.at(-1);
      const resumedAttempt = read(attemptFile(home, resumedID));
      assert.equal(resumedAttempt.status, 'succeeded'); assert.equal(resumedAttempt.predecessor_id, cancelledID);
      assert.notEqual(resumed.policy_decision_id, after.policy_decision_id); assert.equal(hash(partial), partialHash);
      assert.equal(JSON.parse(output(resumedAttempt)).accepted, true);
      const chain = close(continuation);
      report.checks.push({gate: 'interruption_fresh_resume', passed: true, cancelled_attempt: cancelledID, resumed_attempt: resumedID,
        owned_tree_stopped: true, partial_sha256: partialHash, receipt_id: chain.receipt.id});
    } finally { if (!state.done) { child.kill('SIGTERM'); await joined; } }

    const fixtureRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', 'fixtures');
    // Read immutable transition identities from the existing event store; the
    // public CLI remains the consumer and the work lifecycle owns their linkage.
    function transitions() {
      const records = new Map(), events = path.join(home, 'events');
      for (const name of fs.readdirSync(events).filter(n => /^tr_.*\.json$/.test(n))) {
        const tr = read(path.join(events, name));
        assert.equal(name, tr.id + '.json'); records.set(tr.id, tr);
      }
      const segments = path.join(events, 'segments', 'transition');
      if (fs.existsSync(segments)) for (const name of fs.readdirSync(segments).filter(n => n.endsWith('.jsonl'))) {
        for (const line of fs.readFileSync(path.join(segments, name), 'utf8').trim().split('\n').filter(Boolean)) {
          const frame = JSON.parse(line);
          assert.equal(frame.frame_version, 1);
          assert.equal(frame.family, 'transition'); assert.equal(frame.id, frame.record.id);
          records.set(frame.id, frame.record);
        }
      }
      return records;
    }
    const intake = 'work_installed_fixture_intake', before = transitions();
    create(intake, 'Import and independently verify existing synthetic product and accepted decision.');
    const importWorker = "const cp=require('child_process'),a=require('assert/strict');const [bin,product,decision]=process.argv.slice(1);for(const args of [['product','import',product],['decisions','import','--source',decision]]){const r=cp.spawnSync(bin,args,{env:process.env,encoding:'utf8'});a.equal(r.status,0,r.stderr);}console.log('imports executed');";
    cli('governed-fixture-intake', execute(intake, importWorker, [bin,
      path.join(fixtureRoot, 'product', 'market-loop.yaml'), path.join(fixtureRoot, 'brain', 'decision-receipts.md')]));
    const imported = [...transitions()].filter(([id]) => !before.has(id));
    assert(imported.length > 1, 'nonempty real import transitions required');
    for (const [id, tr] of imported) {
      assert.match(id, /^tr_[a-zA-Z0-9_]+$/);
      assert.equal(tr.schema_version, 'compound.transition.v0.1');
      assert.equal(tr.producer.kind, 'cli'); assert.equal(tr.producer.name, 'compound');
      assert(['product-import', 'note-decision-import'].includes(tr.ids.work_id));
      assert(tr.after_refs.length > 0, 'import must produce actual claim refs');
      assert.deepEqual(json(`consume-import-${id}`, ['transition', 'show', id]), tr);
    }
    const product = json('existing-product', ['product', 'where', 'product:focus-notes', '--json']);
    const decision = json('existing-decision', ['decisions', 'show', 'brain-dec-receipts', '--json']);
    assert.equal(product.product_id, 'product:focus-notes');
    assert.equal(decision.lifecycle.status, 'accepted');
    assert.equal(decision.lifecycle.choice, 'Receipts are proof');
    const position = json('existing-position', ['memory', 'position', 'product:focus-notes', '--json']);
    assert.equal(position.schema_version, 'compound.memory_position_fold.v0.2');
    assert.equal(position.entity, 'product:focus-notes');
    const privacy = position.positions.map(x => x.position).find(x => x.predicate === 'privacy_posture');
    assert.equal(privacy?.status, 'active'); assert.equal(privacy?.active_object, 'local_first');
    assert.equal(decision.claim.entity, 'decision:brain-dec-receipts');
    assert.equal(decision.claim.object, 'Receipts are proof');
    assert.equal(decision.claim.state, 'promoted');
    const attach = id => cli(`link-import-${id}`, ['work', 'advance', intake, '--to', 'verifying',
      '--by', actor, '--artifact', id]);
    for (const [id] of imported.slice(0, -1)) attach(id);
    const missingID = imported.at(-1)[0];
    const incomplete = JSON.parse(cli('missing-import-link-falsifier', ['work', 'proof-chain-doctor', '--json'], home, 'nonzero').stdout);
    assert.equal(incomplete.findings.length, 1, 'only the deliberately missing import link may fail');
    assert.equal(incomplete.findings[0].code, 'orphan_transition');
    assert(incomplete.findings[0].message.includes(missingID));
    attach(missingID); const intakeChain = close(intake);
    assert.equal(json('linked-intake-proof-doctor', ['work', 'proof-chain-doctor', '--json']).status, 'ok');
    const afterLink = transitions();
    for (const [id, tr] of imported) assert.deepEqual(afterLink.get(id), tr, 'producer transition remains unchanged');
    report.checks.push({gate: 'governed_intake_lineage', passed: true, transitions: imported.map(([id]) => id),
      missing_link_falsifier: missingID, receipt_id: intakeChain.receipt.id});
    const attempts = json('quiesced-writers', ['run-attempt', 'list', '--json']);
    assert(!attempts.some(a => ['pending', 'running'].includes(a.status)));
    const archive = path.join(root, 'home.tar.gz'), restored = path.join(root, 'restored-home');
    cli('backup', ['home', 'backup', '--home', home, '--target', archive]); cli('verify-archive', ['home', 'verify', archive]);
    fs.renameSync(home, path.join(root, 'source-home-offline')); assert(!fs.existsSync(home));
    cli('isolated-restore', ['home', 'restore', archive, '--target', restored], restored);
    assert.equal(json('restored-health', ['home', 'doctor', '--json'], restored).verdict, 'ok');
    assert.equal(hash(attemptFile(restored, rejectedID)), attemptHash);
    const consumer = 'work_installed_restored_consumer';
    create(consumer, 'Resolve and consume the existing product, local-first constraints and accepted decision from this isolated restored home.', restored);
    const consume = "const cp=require('child_process'),a=require('assert/strict');const [bin]=process.argv.slice(1);const get=args=>{const r=cp.spawnSync(bin,args,{env:process.env,encoding:'utf8'});a.equal(r.status,0);return JSON.parse(r.stdout);};console.log(JSON.stringify({product:get(['product','where','product:focus-notes','--json']),position:get(['memory','position','product:focus-notes','--json']),decision:get(['decisions','show','brain-dec-receipts','--json'])}));";
    cli('restored-real-consumer', execute(consumer, consume, [bin]), restored);
    const consumed = json('restored-work', ['work', 'show', consumer, '--json'], restored), consumerID = consumed.run_attempt_ids.at(-1);
    assert.deepEqual(JSON.parse(output(read(attemptFile(restored, consumerID)))), {product, position, decision});
    const chain = close(consumer, restored);
    assert.equal(json('restored-proof-doctor', ['work', 'proof-chain-doctor', '--json'], restored).status, 'ok');
    assert(!fs.existsSync(home));
    report.checks.push({gate: 'backup_restore_useful_continuity', passed: true, archive_sha256: hash(archive),
      original_home_unavailable: true, product_privacy_positioning_preserved: true, accepted_decision_consumed: true,
      consumer_attempt: consumerID, receipt_id: chain.receipt.id, terminal_proof_green: true});
    report.status = 'passed';
  } catch (error) { report.status = 'failed'; report.error = error.stack; }
  report.finished_at = new Date().toISOString();
  fs.writeFileSync(path.join(root, 'installed-acceptance.json'), JSON.stringify(report, null, 2) + '\n', {flag: 'wx', mode: 0o600});
  return report;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  if (process.argv.length !== 7) {
    console.error('usage: node scripts/installed-release-acceptance.mjs BUNDLE COMMIT ABSENT_EVIDENCE_ROOT PRIOR_BUNDLE PRIOR_COMMIT');
    process.exitCode = 2;
  } else {
    try {
      const result = await acceptInstalled(...process.argv.slice(2));
      console.log(JSON.stringify({status: result.status, report: path.join(process.argv[4], 'installed-acceptance.json')}));
      if (result.status !== 'passed') { console.error(result.error); process.exitCode = 1; }
    } catch (error) { console.error(error.message); process.exitCode = 1; }
  }
}
