// The demo of Agent Desk, for the page that introduces it.
//
//   node demo.mjs fixture <root> <helpers.json>
//       writes a Claude Code config with made-up projects at work, and a month
//       of made-up sessions before today
//   node demo.mjs run <root> <frames dir> <app pid> <clip.exe>
//       drives the app through the story while clip.exe records its window,
//       and notes the moments to keep as still pictures
//
// Everything shown is made up for the purpose: project names, prompts,
// commands, accounts, costs.
import fs from 'node:fs';
import path from 'node:path';
import {spawn} from 'node:child_process';

const [MODE, ROOT] = process.argv.slice(2);
const CONFIG = path.join(ROOT, 'claude');
const sleep = ms => new Promise(r => setTimeout(r, ms));

// ---- transcripts ---------------------------------------------------------------

const slug = dir => dir.replace(/[^a-zA-Z0-9]/g, '-');
const iso = msAgo => new Date(Date.now() - msAgo).toISOString();
const MIN = 60_000;
const ACCOUNT_A = 'aaaaaaaa-1111-4000-8000-00000000000a';
const ACCOUNT_B = 'bbbbbbbb-2222-4000-8000-00000000000b';

function usage(output, written, read) {
    return {
        input_tokens: 4, output_tokens: output, cache_creation_input_tokens: written, cache_read_input_tokens: read,
        cache_creation: {ephemeral_5m_input_tokens: 0, ephemeral_1h_input_tokens: written},
        output_tokens_details: {thinking_tokens: Math.round(output * 0.45)},
    };
}

let serial = 0;
function said(side, model, msAgo, block, use, stop = 'tool_use', extra = {}) {
    return {
        type: 'assistant', isSidechain: side, timestamp: iso(msAgo), effort: 'xhigh',
        message: {id: `msg_demo_fx_${++serial}`, model, stop_reason: stop, content: [block], usage: use, ...extra},
    };
}
const text = t => ({type: 'text', text: t});
const tool = (id, name, input) => ({type: 'tool_use', id, name, input});
const result = (side, msAgo, id, content, extra = {}) => ({
    type: 'user', isSidechain: side, timestamp: iso(msAgo),
    message: {role: 'user', content: [{type: 'tool_result', tool_use_id: id, content}]}, ...extra,
});
const asked = (side, msAgo, content) => ({
    type: 'user', isSidechain: side, timestamp: iso(msAgo), permissionMode: 'auto',
    origin: {kind: 'human'}, message: {role: 'user', content},
});
const owner = account => ({type: 'session-owner', accountUuid: account, organizationUuid: 'demo-org'});
const allowed = reasonType => ({permissionDecision: {decision: 'accept', source: 'config', reasonType}});

// What Claude Code writes in the model's place when a usage limit is used up.
function refused(side, msAgo, kind, resetsAt) {
    const info = {status: 'rejected', rateLimitType: kind, resetsAt: Math.floor(resetsAt / 1000)};
    return {
        type: 'assistant', isSidechain: side, timestamp: iso(msAgo), error: 'rate_limit', isApiErrorMessage: true, apiErrorStatus: 429,
        apiErrorParams: {rate_limit_info: info}, quotaLimits: info,
        message: {
            id: `msg_demo_fx_${++serial}`, model: '<synthetic>', role: 'assistant', stop_reason: 'stop_sequence',
            content: [text('You\'ve hit your 5-hour limit')], usage: usage(0, 0, 0),
        },
    };
}

function put(file, lines, mtime) {
    fs.mkdirSync(path.dirname(file), {recursive: true});
    fs.writeFileSync(file, lines.map(l => JSON.stringify(l)).join('\n') + '\n');
    if (mtime) fs.utimesSync(file, mtime, mtime);
}
const add = (file, lines) => fs.appendFileSync(file, lines.map(l => JSON.stringify(l)).join('\n') + '\n');

const transcriptOf = (cwd, sid) => path.join(CONFIG, 'projects', slug(cwd), sid + '.jsonl');
const agentOf = (cwd, sid, id, wf = '') =>
    path.join(CONFIG, 'projects', slug(cwd), sid, 'subagents', ...(wf ? ['workflows', wf] : []), `agent-${id}.jsonl`);

function agentFiles(cwd, sid, id, meta, lines, wf = '') {
    const file = agentOf(cwd, sid, id, wf);
    put(file, lines);
    fs.writeFileSync(file.replace(/\.jsonl$/, '.meta.json'), JSON.stringify(meta));
}

// A run of earlier responses, spread over the minutes before now, so that a
// session has a past: a pulse to draw and a cost to show.
function history(side, model, fromMin, toMin, count, use) {
    const out = [];
    for (let i = 0; i < count; i++) {
        const at = fromMin - (fromMin - toMin) * (i / Math.max(1, count - 1));
        out.push(said(side, model, at * MIN, {type: 'thinking', thinking: ''}, use));
    }
    return out;
}

// A small, fixed sequence of numbers, so that every recording draws the same
// month.
let seed = 7;
const rand = () => ((seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648);

// The month before today: sessions that closed on earlier days, which the
// History page adds up.
function pastMonth(projects) {
    const models = [
        ['claude-opus-5-5', usage(700, 4000, 120000)],
        ['claude-fable-5-2', usage(900, 6000, 160000)],
        ['claude-sonnet-5-5', usage(600, 3000, 80000)],
    ];
    for (let d = 1; d <= 34; d++) {
        const day = new Date();
        day.setDate(day.getDate() - d);
        const weekend = day.getDay() === 0 || day.getDay() === 6;
        const count = weekend ? (rand() < 0.4 ? 1 : 0) : 1 + Math.floor(rand() * 3);
        for (let s = 0; s < count; s++) {
            const cwd = projects[Math.floor(rand() * projects.length)];
            const [model, use] = models[Math.floor(rand() * (rand() < 0.5 ? 2 : 3))];
            const start = new Date(day);
            start.setHours(9 + Math.floor(rand() * 8), Math.floor(rand() * 60), 0, 0);
            const responses = 12 + Math.floor(rand() * 60);
            const sid = `past-${d}-${s}`;
            const lines = [owner(ACCOUNT_A)];
            for (let i = 0; i < responses; i++) {
                const at = new Date(start.getTime() + i * 75_000);
                lines.push({
                    type: 'assistant', isSidechain: false, timestamp: at.toISOString(), cwd,
                    message: {id: `msg_past_${d}_${s}_${i}`, model, stop_reason: i === responses - 1 ? 'end_turn' : 'tool_use', content: [text('…')], usage: use},
                });
            }
            put(transcriptOf(cwd, sid), lines, new Date(start.getTime() + responses * 75_000));
        }
    }
}

function fixture(helpers) {
    const pay = path.join(ROOT, 'payments-api');
    const web = path.join(ROOT, 'web-portal');
    const mob = path.join(ROOT, 'mobile-app');
    const ord = path.join(ROOT, 'order-service');
    const risk = path.join(ROOT, 'risk-engine');
    for (const dir of [pay, web, mob, risk, path.join(ord, 'internal', 'order'), path.join(CONFIG, 'sessions')]) {
        fs.mkdirSync(dir, {recursive: true});
    }
    const register = (helper, sid, cwd, status, name, startedMinAgo, extra = {}) => fs.writeFileSync(
        path.join(CONFIG, 'sessions', `${helper.pid}.json`),
        JSON.stringify({
            pid: helper.pid, sessionId: sid, cwd, status, name, procStart: helper.procStart,
            entrypoint: 'claude-vscode', version: '2.1.322', startedAt: Date.now() - startedMinAgo * MIN,
            updatedAt: Date.now() - 2 * MIN, statusUpdatedAt: Date.now() - 2 * MIN, ...extra,
        }));

    pastMonth([pay, web, mob, ord, risk]);

    // payments-api: a session at work, with reviewers and a workflow run. Its
    // past holds edits, tests and a commit, for its summary.
    register(helpers[0], 'demo-a', pay, 'busy', 'payments-api-1', 52);
    const fable = usage(900, 6000, 180000);
    const handler = path.join(pay, 'internal', 'transfer', 'handler.go');
    const keys = path.join(pay, 'internal', 'transfer', 'idempotency.go');
    put(transcriptOf(pay, 'demo-a'), [
        {type: 'ai-title', aiTitle: 'Thêm idempotency cho API chuyển tiền', sessionId: 'demo-a'},
        owner(ACCOUNT_A),
        asked(false, 51 * MIN, 'Thêm idempotency key cho API chuyển tiền, rồi cho review kỹ phần ledger'),
        ...history(false, 'claude-fable-5-2', 50, 44, 8, fable),
        said(false, 'claude-fable-5-2', 43 * MIN, tool('pa-w1', 'Write', {file_path: keys, content: 'package transfer\n'}), fable),
        result(false, 42.8 * MIN, 'pa-w1', 'File created', {
            ...allowed('mode'),
            toolUseResult: {type: 'create', filePath: keys, content: 'package transfer\n\n// Keys remembers the requests already done.\ntype Keys struct {\n\tdb *sql.DB\n}\n\nfunc (k *Keys) Seen(ctx context.Context, key string) (bool, error) {\n\treturn false, nil\n}\n', structuredPatch: []},
        }),
        said(false, 'claude-fable-5-2', 41 * MIN, tool('pa-e1', 'Edit', {file_path: handler, old_string: 'func (h *Handler) Transfer(', new_string: 'func (h *Handler) Transfer('}), fable),
        result(false, 40.8 * MIN, 'pa-e1', 'ok', {
            ...allowed('classifier'),
            toolUseResult: {filePath: handler, structuredPatch: [{lines: [' func (h *Handler) Transfer(w http.ResponseWriter, r *http.Request) {', '+\tkey := r.Header.Get("Idempotency-Key")', '+\tif seen, err := h.keys.Seen(r.Context(), key); err != nil || seen {', '+\t\th.replay(w, key)', '+\t\treturn', '+\t}', '-\t// TODO: idempotency']}]},
        }),
        said(false, 'claude-fable-5-2', 39 * MIN, tool('pa-b1', 'Bash', {command: 'go test ./internal/transfer/... -race', description: 'Chạy test của transfer'}), fable),
        result(false, 38 * MIN, 'pa-b1', 'ok  \tpayments-api/internal/transfer\t2.114s', allowed('classifier')),
        said(false, 'claude-fable-5-2', 36 * MIN, tool('pa-b2', 'Bash', {command: 'git commit -am "transfer: idempotency key cho API chuyển tiền"', description: 'Commit thay đổi'}), fable),
        result(false, 35.8 * MIN, 'pa-b2', '[feature/idempotency 4f2a9c1] transfer: idempotency key cho API chuyển tiền', allowed('classifier')),
        {type: 'system', subtype: 'compact_boundary', timestamp: iso(34 * MIN), compactMetadata: {trigger: 'auto', preTokens: 182400, postTokens: 23800}},
        {type: 'user', timestamp: iso(34 * MIN), isCompactSummary: true, message: {role: 'user', content: 'Tóm tắt phần trước…'}},
        said(false, 'claude-fable-5-2', 33 * MIN, text('Phần idempotency đã xong. Tôi cho hai reviewer soát song song.'), fable, 'tool_use',
            {diagnostics: {cache_miss_reason: {type: 'previous_message_not_found', cache_missed_input_tokens: 41200}}}),
        ...history(false, 'claude-fable-5-2', 32, 2, 12, fable),
        said(false, 'claude-fable-5-2', 40_000, tool('ta1', 'Agent', {description: 'Go review'}), fable),
        {type: 'last-prompt', lastPrompt: 'Thêm idempotency key cho API chuyển tiền, rồi cho review kỹ phần ledger', sessionId: 'demo-a'},
    ]);
    const opus = usage(400, 1500, 60000);
    agentFiles(pay, 'demo-a', 'cr', {agentType: 'code-reviewer', description: 'Code review', requestShape: 'background'}, [
        asked(true, 13 * MIN, 'Review thay đổi ở internal/transfer: tính đúng, quy ước của repo, lỗi dễ thấy.'),
        ...history(true, 'claude-opus-5-5', 12.5, 1, 14, opus),
        said(true, 'claude-opus-5-5', 30_000, text('Phần handler ổn. Tôi xem tiếp chỗ gọi sang ledger.'), opus),
        said(true, 'claude-opus-5-5', 12_000, tool('cr1', 'Grep', {pattern: 'IdempotencyKey', path: 'internal'}), opus),
    ]);
    agentFiles(pay, 'demo-a', 'go', {agentType: 'go-reviewer', description: 'Go review', requestShape: 'background'}, [
        asked(true, 9 * MIN, 'Soát mã Go ở internal/ledger: context, transaction, race trên số dư.'),
        ...history(true, 'claude-opus-5-5', 8.5, 1.5, 9, opus),
        said(true, 'claude-opus-5-5', 70_000, text('Tôi đọc phần ghi sổ trước, rồi chạy vet và test có bật race.'), opus),
        said(true, 'claude-opus-5-5', 60_000, tool('go1', 'Read', {file_path: path.join(pay, 'internal', 'ledger', 'post.go')}), opus),
        result(true, 58_000, 'go1', 'package ledger\n\nfunc (l *Ledger) Post(ctx context.Context, e Entry) error {\n\ttx, err := l.db.BeginTx(ctx, nil)'),
        said(true, 'claude-opus-5-5', 40_000, tool('go2', 'Bash', {command: 'go vet ./internal/ledger/...', description: 'Kiểm tra mã ledger'}), opus),
        result(true, 31_000, 'go2', 'ok'),
        said(true, 'claude-opus-5-5', 9_000, tool('go3', 'Read', {file_path: path.join(pay, 'internal', 'ledger', 'balance.go')}), opus),
    ]);
    agentFiles(pay, 'demo-a', 'sec', {agentType: 'security-audit', description: 'Security audit', requestShape: 'background', finished: true}, [
        asked(true, 20 * MIN, 'Soát bảo mật endpoint chuyển tiền.'),
        ...history(true, 'claude-opus-5-5', 19, 7, 11, opus),
        said(true, 'claude-opus-5-5', 6 * MIN, tool('sec9', 'SubagentHandback', {message: 'Không có lỗi chặn. Idempotency key được kiểm tra bên trong transaction.'}), opus),
    ]);

    // A workflow run of the same session: three of its agents have reported,
    // two are still reading.
    const sonnetSmall = usage(300, 1200, 40000);
    const wf = 'wf_ledger-audit';
    const wfAgent = (id, desc, phase, minAgo, lines, done) => agentFiles(pay, 'demo-a', id,
        {agentType: 'workflow-subagent', description: desc, workflowPhase: phase, ...(done ? {finished: true} : {})}, lines, wf);
    wfAgent('w1', 'đọc: ledger/post.go', 'Đọc mã', 18, [
        asked(true, 18 * MIN, 'Đọc internal/ledger/post.go và ghi lại mọi chỗ ghi số dư.'),
        ...history(true, 'claude-sonnet-5-5', 17.5, 12, 6, sonnetSmall),
        said(true, 'claude-sonnet-5-5', 11 * MIN, tool('w1r', 'StructuredOutput', {file: 'internal/ledger/post.go', writes: ['Post', 'Reverse'], risk: 'thấp'}), sonnetSmall),
    ], true);
    wfAgent('w2', 'đọc: ledger/balance.go', 'Đọc mã', 18, [
        asked(true, 18 * MIN, 'Đọc internal/ledger/balance.go và ghi lại mọi chỗ ghi số dư.'),
        ...history(true, 'claude-sonnet-5-5', 17.5, 10, 7, sonnetSmall),
        said(true, 'claude-sonnet-5-5', 9.5 * MIN, tool('w2r', 'StructuredOutput', {file: 'internal/ledger/balance.go', writes: ['Debit', 'Credit', 'Hold'], risk: 'trung bình'}), sonnetSmall),
    ], true);
    wfAgent('w3', 'đọc: transfer/retry.go', 'Đọc mã', 18, [
        asked(true, 18 * MIN, 'Đọc internal/transfer/retry.go.'),
        ...history(true, 'claude-sonnet-5-5', 17.5, 9, 6, sonnetSmall),
        said(true, 'claude-sonnet-5-5', 8.5 * MIN, tool('w3r', 'StructuredOutput', {file: 'internal/transfer/retry.go', writes: [], risk: 'thấp'}), sonnetSmall),
    ], true);
    wfAgent('w4', 'soát: khoá số dư', 'Soát', 8, [
        asked(true, 8 * MIN, 'Kiểm tra mọi chỗ ghi số dư đều khoá dòng trong transaction.'),
        ...history(true, 'claude-sonnet-5-5', 7.5, 0.5, 9, sonnetSmall),
        said(true, 'claude-sonnet-5-5', 15_000, tool('w4a', 'Grep', {pattern: 'FOR UPDATE', path: 'internal/ledger'}), sonnetSmall),
    ]);
    wfAgent('w5', 'soát: idempotency', 'Soát', 8, [
        asked(true, 8 * MIN, 'Kiểm tra idempotency key được ghi trong cùng transaction với bút toán.'),
        ...history(true, 'claude-sonnet-5-5', 7.5, 0.5, 8, sonnetSmall),
        said(true, 'claude-sonnet-5-5', 20_000, tool('w5a', 'Read', {file_path: keys}), sonnetSmall),
    ]);

    // web-portal: a session that stopped eleven minutes ago to ask a question.
    register(helpers[1], 'demo-b', web, 'waiting', 'web-portal-1', 95,
        {waitingFor: 'input needed', updatedAt: Date.now() - 11 * MIN, statusUpdatedAt: Date.now() - 11 * MIN});
    const sonnet = usage(700, 3000, 90000);
    put(transcriptOf(web, 'demo-b'), [
        {type: 'ai-title', aiTitle: 'Sửa lỗi form đăng nhập trên Safari', sessionId: 'demo-b'},
        owner(ACCOUNT_A),
        asked(false, 52 * MIN, 'Form đăng nhập không bấm được trên Safari'),
        ...history(false, 'claude-sonnet-5-5', 51, 12, 18, sonnet),
        said(false, 'claude-sonnet-5-5', 11 * MIN, tool('wq1', 'AskUserQuestion', {questions: [{
            question: 'Bật lại nút Đăng nhập khi ô mật khẩu mất focus, hay để nút luôn bật và kiểm tra lúc bấm?',
            header: 'Cách sửa', multiSelect: false,
            options: [{label: 'Khi mất focus', description: 'Ít thay đổi nhất'}, {label: 'Luôn bật', description: 'Kiểm tra khi bấm'}],
        }]}), sonnet),
        {type: 'last-prompt', lastPrompt: 'Form đăng nhập không bấm được trên Safari', sessionId: 'demo-b'},
    ]);

    // mobile-app: a session on another account, stopped by its five-hour
    // limit, with the agent it had started.
    const lifts = new Date(Date.now() + 40 * MIN);
    lifts.setMinutes(0, 0, 0);
    if (lifts.getTime() < Date.now() + 15 * MIN) lifts.setHours(lifts.getHours() + 1);
    register(helpers[2], 'demo-c', mob, 'idle', 'mobile-app-1', 31);
    const opusMain = usage(800, 5000, 150000);
    put(transcriptOf(mob, 'demo-c'), [
        {type: 'ai-title', aiTitle: 'Nâng React Native lên 0.82', sessionId: 'demo-c'},
        owner(ACCOUNT_B),
        asked(false, 30 * MIN, 'Nâng React Native lên 0.82 và kiểm tra thư viện nào có lỗ hổng'),
        ...history(false, 'claude-opus-5-5', 29, 5, 12, opusMain),
        said(false, 'claude-opus-5-5', 4.4 * MIN, tool('tc1', 'Agent', {description: 'Kiểm tra lỗ hổng thư viện'}), opusMain),
        refused(false, 3 * MIN, 'five_hour', lifts.getTime()),
        {type: 'last-prompt', lastPrompt: 'Nâng React Native lên 0.82 và kiểm tra thư viện nào có lỗ hổng', sessionId: 'demo-c'},
    ]);
    const small = usage(300, 1200, 40000);
    agentFiles(mob, 'demo-c', 'dep', {agentType: 'dependency-vuln-reviewer', description: 'Kiểm tra lỗ hổng thư viện', requestShape: 'background'}, [
        asked(true, 4.3 * MIN, 'Kiểm tra advisory cho các thư viện trong package.json.'),
        ...history(true, 'claude-sonnet-5-5', 4.2, 3.6, 4, small),
        said(true, 'claude-sonnet-5-5', 3.4 * MIN, tool('dep1', 'Bash', {command: 'npm audit --omit=dev'}), small),
        result(true, 3.2 * MIN, 'dep1', 'found 2 vulnerabilities (1 moderate, 1 high)'),
        refused(true, 3.1 * MIN, 'five_hour', lifts.getTime()),
    ]);
}

// ---- driving the app -------------------------------------------------------------

const PORT = Number(process.env.MONITOR_AGENT_E2E_PORT ?? 47833);
const TOKEN = process.env.MONITOR_AGENT_E2E_TOKEN ?? '';

async function post(pathname, body = '') {
    const res = await fetch(`http://127.0.0.1:${PORT}${pathname}`, {method: 'POST', headers: {'X-E2E-Token': TOKEN}, body});
    const out = await res.text();
    if (!res.ok) throw new Error(`${pathname}: ${res.status} ${out.trim()}`);
    return out;
}

async function js(expression) {
    const out = JSON.parse(await post('/eval', expression));
    if (out.error) throw new Error('page script failed: ' + out.error);
    return out.value;
}

async function waitFor(expression, what, timeoutMs = 20000) {
    const end = Date.now() + timeoutMs;
    for (;;) {
        if (await js(expression)) return;
        if (Date.now() > end) throw new Error('timed out waiting for ' + what);
        await sleep(100);
    }
}

const q = s => JSON.stringify(s);
const click = sel => js(`(() => { const el = document.querySelector(${q(sel)}); if (!el) return false; el.click(); return true; })()`);
const showing = what => waitFor(`document.querySelector('.chat').dataset.showing === ${q(what)} && !document.querySelector('.chat').hidden`, 'the panel to show ' + what);
const scrollTo = (sel, block = 'center') => js(`(() => { const el = document.querySelector(${q(sel)}); if (!el) return false; el.scrollIntoView({block: ${q(block)}, behavior: 'smooth'}); return true; })()`);

async function run() {
    const frames = process.argv[4];
    const pid = process.argv[5];
    const clip = process.argv[6];
    if (!clip || !fs.existsSync(clip)) throw new Error('no recorder at ' + clip);
    const pay = path.join(ROOT, 'payments-api');

    await sleep(4000);
    for (let i = 0; ; i++) {
        try {
            if (await js(`document.readyState === 'complete' && typeof window.go === 'object'`)) break;
        } catch { /* not up yet */ }
        if (i > 60) throw new Error('the page of the app could not be reached');
        await sleep(500);
    }
    await post('/size?w=1280&h=800');
    await waitFor(`document.querySelectorAll('.bay').length >= 3`, 'the projects', 40000);
    // The month before today is read in the background; it should be in the
    // History page before the recording reaches it.
    await sleep(6000);

    // The reviewers and the workflow go on working while the clip is
    // recorded: every so often one of them gets the result of its last call
    // and makes the next.
    const goSteps = [
        ['Grep', {pattern: 'SELECT .* FOR UPDATE', path: 'internal/ledger'}, 'internal/ledger/balance.go:41:\trow := tx.QueryRowContext(ctx, `SELECT amount FROM balances WHERE account_id = $1 FOR UPDATE`, id)'],
        ['Bash', {command: 'go test ./internal/ledger/ -run TestPost -race', description: 'Chạy test ghi sổ với race detector'}, 'ok  \tpayments-api/internal/ledger\t1.284s'],
        ['Read', {file_path: path.join(pay, 'internal', 'ledger', 'post_test.go')}, 'package ledger\n\nfunc TestPostIsAtomic(t *testing.T) {'],
        ['Grep', {pattern: 'context.Background\\(\\)', path: 'internal'}, 'internal/transfer/retry.go:27:\tctx := context.Background()'],
        ['Read', {file_path: path.join(pay, 'internal', 'transfer', 'retry.go')}, 'package transfer\n\nfunc (r *Retrier) Do(fn func(context.Context) error) error {'],
        ['Bash', {command: 'go vet ./internal/transfer/...', description: 'Kiểm tra mã transfer'}, 'ok'],
    ];
    const crSteps = [
        ['Read', {file_path: path.join(pay, 'internal', 'transfer', 'handler.go')}, 'package transfer'],
        ['Grep', {pattern: 'TODO|FIXME', path: 'internal/transfer'}, 'internal/transfer/handler.go:88:\t// TODO: trả 409 khi key trùng nhưng nội dung khác'],
        ['Read', {file_path: path.join(pay, 'internal', 'transfer', 'handler_test.go')}, 'package transfer'],
        ['Bash', {command: 'git diff --stat main', description: 'Xem các file đã đổi'}, ' internal/transfer/handler.go | 31 ++++++--\n internal/ledger/post.go      | 12 ++-'],
    ];
    const wfSteps = [
        ['Grep', {pattern: 'UPDATE balances', path: 'internal'}, 'internal/ledger/balance.go:58'],
        ['Read', {file_path: path.join(pay, 'internal', 'ledger', 'hold.go')}, 'package ledger'],
        ['Grep', {pattern: 'BeginTx', path: 'internal'}, 'internal/ledger/post.go:12'],
    ];
    const opus = usage(400, 1500, 60000);
    const sonnetSmall = usage(300, 1200, 40000);
    let beat = 0;
    const last = {go: 'go3', cr: 'cr1', w4: 'w4a', w5: 'w5a'};
    const step = (who, file, model, use, steps, i) => {
        const [name, input, res] = steps[i % steps.length];
        const id = `${who}-live-${beat}`;
        add(file, [result(true, 0, last[who], res), said(true, model, 0, tool(id, name, input), use)]);
        last[who] = id;
    };
    const ticker = setInterval(() => {
        beat++;
        switch (beat % 4) {
            case 1: step('go', agentOf(pay, 'demo-a', 'go'), 'claude-opus-5-5', opus, goSteps, beat); break;
            case 2: step('cr', agentOf(pay, 'demo-a', 'cr'), 'claude-opus-5-5', opus, crSteps, beat); break;
            case 3: step('w4', agentOf(pay, 'demo-a', 'w4', 'wf_ledger-audit'), 'claude-sonnet-5-5', sonnetSmall, wfSteps, beat); break;
            default: step('w5', agentOf(pay, 'demo-a', 'w5', 'wf_ledger-audit'), 'claude-sonnet-5-5', sonnetSmall, wfSteps, beat + 1); break;
        }
    }, 1300);

    // The pointer the clip will show. The app is driven without a mouse; the
    // places the demo "clicks" are written down for the clip maker to draw.
    const track = {dpr: await js('devicePixelRatio'), events: [], cut: [], shots: [], split: 0};
    async function pointAt(sel, dur = 560) {
        const at = await js(`(() => { const el = document.querySelector(${q(sel)}); const b = el.getBoundingClientRect(); return {x: b.left + Math.min(b.width / 2, 90), y: b.top + b.height / 2}; })()`);
        track.events.push({t: Date.now(), kind: 'move', x: at.x, y: at.y, dur, space: 'page'});
        await sleep(dur + 140);
    }
    async function press() {
        track.events.push({t: Date.now(), kind: 'click'});
        await sleep(170);
    }
    // A still picture of this moment, taken from the recording afterwards.
    async function shot(name) {
        await sleep(400);
        track.shots.push({name, t: Date.now()});
    }
    async function openTrail(target) {
        const row = `li.strip[data-open-trail=${q(target)}]`;
        await scrollTo(row);
        await sleep(700);
        await pointAt(row + ' .strip-open');
        await press();
        await click(row);
        await showing('trail');
    }
    async function closePanel() {
        await pointAt('.chat-close');
        await press();
        await click('.chat-close');
        await sleep(700);
    }

    // The page opens wherever it was last scrolled to; the story starts at
    // its top, with the desks and the cost counter.
    await js(`(() => { window.scrollTo({top: 0}); return true; })()`);
    await sleep(500);

    fs.rmSync(frames, {recursive: true, force: true});
    fs.mkdirSync(frames, {recursive: true});
    const rec = spawn(clip, ['rec', '-pid', pid, '-dir', frames, '-fps', '10'], {stdio: 'ignore'});
    const recDone = new Promise(r => rec.on('exit', r));
    await sleep(900);

    // ── Part one: watching ──────────────────────────────────────────────────
    // 1. The floor: who is at work, who waits, who is stopped by a limit.
    await sleep(1800);
    await shot('top');
    await sleep(1200);

    // 2. A reviewer's steps, the commands it runs as it runs them.
    await openTrail('demo-a|go');
    await sleep(2600);
    await shot('trail');
    await sleep(1200);
    await closePanel();

    // 3. The workflow, as one group; its finished agents unfold.
    const wfHead = '.wf-toggle[data-toggle-session="demo-a|wf_ledger-audit"]';
    await scrollTo(wfHead);
    await sleep(900);
    await pointAt(wfHead);
    await press();
    await click(wfHead);
    await sleep(1600);
    await shot('workflow');
    await sleep(800);

    // 4. What the session did, added up.
    await openTrail('demo-a|');
    await waitFor(`!document.querySelector('.chat-summary').hidden`, 'the summary', 20000);
    await sleep(600);
    await pointAt('.chat-summary-head');
    await press();
    await js(`(() => { document.querySelector('.chat-summary').open = true; return true; })()`);
    await sleep(2000);
    await shot('summary');
    await sleep(1400);
    await closePanel();

    // 5. Those that need you: the question waiting eleven minutes, the
    //    session stopped by a limit.
    await scrollTo('li.strip[data-open-trail="demo-b|"]', 'start');
    await sleep(1600);
    await shot('attention');
    await sleep(1200);

    // 6. The month, day by day.
    await js(`window.scrollTo({top: 0, behavior: 'smooth'})`);
    await sleep(700);
    await pointAt('[data-view="history"].view-tab');
    await press();
    await click('[data-view="history"].view-tab');
    await waitFor(`document.querySelectorAll('.hc-col').length >= 28`, 'the history chart', 20000);
    await sleep(1500);
    await shot('history');
    await sleep(1500);
    await pointAt('[data-view="board"].view-tab');
    await press();
    await click('[data-view="board"].view-tab');
    await sleep(1200);

    // ── Part two: working in the app ─────────────────────────────────────────
    track.split = Date.now();
    await sleep(500);

    // 7. Add a project. The folder dialog of Windows is not recorded; the
    //    question that follows it is.
    await js(`window.scrollTo({top: 0})`);
    await sleep(400);
    await pointAt('.desk-add .desk-plus');
    await press();
    await post('/trust?dir=' + encodeURIComponent(path.join(ROOT, 'order-service')));
    const dialogFile = path.join(frames, 'dialog.json');
    for (let i = 0; !fs.existsSync(dialogFile); i++) {
        if (i > 100) throw new Error('the trust question did not appear');
        await sleep(100);
    }
    await sleep(2000);
    const dialog = JSON.parse(fs.readFileSync(dialogFile, 'utf8'));
    const yes = dialog.buttons.find(b => b.id === 6);
    track.events.push({t: Date.now(), kind: 'move', x: yes.x, y: yes.y, dur: 560, space: 'frame'});
    await sleep(700);
    await press();
    fs.writeFileSync(path.join(frames, 'press-6'), '');
    await waitFor(`Array.from(document.querySelectorAll('.bay-name')).some(b => b.textContent.includes('order-service'))`, 'the new project on the board');
    await sleep(1100);

    // 8. A session of the app's own, and a task for it.
    const fresh = '[data-new-session][data-project-name="order-service"]';
    await scrollTo(fresh);
    await sleep(500);
    await pointAt(fresh);
    await press();
    await click(fresh);
    await showing('draft');
    await sleep(1300);
    await pointAt('.chat-input');
    await press();
    for (const ch of 'Chống tạo lệnh trùng khi client gửi lại cùng idempotency key, rồi chạy test.') {
        await js(`(() => { const el = document.querySelector('.chat-input'); el.focus(); el.value += ${q(ch)}; el.dispatchEvent(new Event('input', {bubbles: true})); return true; })()`);
        await sleep(22);
    }
    await sleep(600);
    await js(`document.querySelector('.chat-input').dispatchEvent(new KeyboardEvent('keydown', {key: 'Enter', bubbles: true, cancelable: true}))`);

    // 9. It asks before it changes anything: first the edit, then the tests.
    const allow = '.permit [data-permit="allow"]';
    for (const toolName of ['Edit', 'Bash']) {
        await waitFor(`document.querySelector('.permit-title')?.textContent.includes(${q(toolName)})`, `the request to use ${toolName}`, 30000);
        await sleep(toolName === 'Edit' ? 2600 : 1600);
        if (toolName === 'Edit') await shot('permit');
        // A request longer than its box has to be read to the end first.
        await js(`(() => { document.querySelector('.permit-body').scrollTo({top: 99999, behavior: 'smooth'}); return true; })()`);
        await sleep(800);
        await waitFor(`!document.querySelector(${q(allow)}).disabled`, 'the allow button');
        await pointAt(allow, 480);
        await press();
        await click(allow);
    }
    await waitFor(`document.querySelector('.chat-state.is-idle') !== null`, 'the session to finish', 40000);
    await sleep(2200);
    await shot('chat');
    await sleep(800);

    // 10. Back on the board: four projects now.
    await closePanel();
    await js(`window.scrollTo({top: 0})`);
    await sleep(2600);

    clearInterval(ticker);
    fs.writeFileSync(path.join(frames, 'stop'), '');
    await Promise.race([recDone, sleep(5000)]);
    fs.writeFileSync(path.join(frames, 'track.json'), JSON.stringify(track));
    console.log(`recorded: ${fs.readdirSync(frames).filter(f => f.startsWith('f-')).length} distinct frames`);
}

if (MODE === 'fixture') {
    fixture(JSON.parse(fs.readFileSync(process.argv[4], 'utf8').replace(/^﻿/, '')));
    console.log('fixture written under ' + CONFIG);
} else {
    run().then(() => process.exit(0), err => {
        console.log('DEMO ABORTED: ' + err.message);
        try { fs.writeFileSync(path.join(process.argv[4], 'stop'), ''); } catch { /* nothing to stop */ }
        setTimeout(() => process.exit(1), 1500);
    });
}
