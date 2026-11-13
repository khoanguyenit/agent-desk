import {
    AnswerPermission, EndSession, ForgetSession, GetChat, GetClaude, GetModels, GetTrail, InterruptSession, ListChats,
    ResumeSession, SendChat, StartSession, TrustResume,
} from '../wailsjs/go/main/App';
import {agent, monitor} from '../wailsjs/go/models';
import {EventsOn} from '../wailsjs/runtime/runtime';
import * as f from './format';
import {markdown} from './markdown';
import {agentStatus, h, OpenMark, sessionStatus, statusNode, StatusView} from './render';

// The side panel. It shows one of three things:
//
//   - a conversation with a Claude Code session that this app started, where
//     the user types requests and answers the agent's requests for permission;
//   - the draft of a new session, before its first request is sent;
//   - the trail of an agent or session that runs elsewhere: its latest steps,
//     to be read only.
//
// It lives outside the part of the page that is redrawn every two seconds, so
// typing is never interrupted. Sessions running in VS Code can be watched
// here but not talked to, on purpose: a conversation has one home, and theirs
// is the editor.

type PermMode = 'ask' | 'acceptEdits' | 'plan';
type Showing = 'none' | 'draft' | 'chat' | 'trail';

interface Draft {
    projectPath: string;
    projectName: string;
    // Set when the draft carries on a session that has ended instead of
    // starting a new one.
    resume?: { sessionId: string; title: string };
}

export interface TrailTarget {
    sessionId: string;
    agentId: string;
}

const MODES: { value: PermMode; label: string; hint: string }[] = [
    {value: 'ask', label: 'Hỏi trước khi sửa file hay chạy lệnh', hint: 'Mỗi thao tác có thay đổi đều chờ bạn bấm Cho phép.'},
    {value: 'acceptEdits', label: 'Tự sửa file, hỏi trước khi chạy lệnh', hint: 'Sửa file trong project không hỏi lại. Chạy lệnh vẫn chờ bạn.'},
    {value: 'plan', label: 'Chỉ đọc và lập kế hoạch', hint: 'Agent đọc code rồi đề xuất kế hoạch, không sửa gì.'},
];

const STATE: Record<string, StatusView> = {
    starting: {cls: 'is-running', icon: 'dot', label: 'Đang khởi động'},
    working: {cls: 'is-running', icon: 'dot', label: 'Đang chạy'},
    waiting: {cls: 'is-quiet', icon: 'pause', label: 'Chờ bạn cho phép'},
    idle: {cls: 'is-idle', icon: 'ring', label: 'Rảnh'},
    ended: {cls: 'is-done', icon: 'check', label: 'Đã kết thúc'},
    failed: {cls: 'is-stopped', icon: 'square', label: 'Dừng do lỗi'},
};

// How long a permission card's buttons stay inactive after the card appears
// or moves: a click meant for something else must not land on them.
const ARM_MS = 600;

let showing: Showing = 'none';
let currentId = '';
let draft: Draft | null = null;
let target: TrailTarget | null = null;
let trail: monitor.Trail | null = null;
let trailProblem = '';
let view: agent.View | null = null;
let items = new Map<number, agent.Item>();
let rev = 0;
// gen changes every time the panel turns to something else. A reply that was
// asked for under an older gen is for a view that is gone, and is dropped.
let gen = 0;
let fetching = false;
let again = false;
let sending = false;
// A session is being checked for carrying on: the question whether its folder
// is trusted may be on screen.
let resuming = false;
let problem = '';
let board: monitor.Snapshot | null = null;
// privacy mirrors the "Ẩn nội dung" switch. It covers what the panel shows of
// sessions that run elsewhere, never the chat of the app's own.
let privacy = false;
let claude: { path: string; problem: string } | null = null;
let models: monitor.ModelChoice[] = [];
let modelShape = '';
let pickerShape = '';
let pickerAsk = 0;
let onChange: () => void = () => {};
const answering = new Set<string>();
// Which tool calls the user opened or closed by hand, by entry key.
const toggled = new Map<string, boolean>();
// Stable keys for trail steps, which carry no id of their own.
let trailKeys = new Map<string, number>();

// ---- the panel: built once -----------------------------------------------------

const projectEl = h('p', {class: 'chat-project'});
const titleEl = h('h2', {class: 'chat-title'});
const metaEl = h('p', {class: 'chat-meta'});
const picker = h('select', {class: 'chat-pick', 'aria-label': 'Chuyển sang session khác của app', title: 'Các session do app mở'});
const stopBtn = h('button', {type: 'button', class: 'chat-btn', title: 'Dừng lượt đang chạy. Session vẫn mở để bạn hỏi tiếp.'}, 'Dừng lượt này');
const endBtn = h('button', {type: 'button', class: 'chat-btn chat-btn-quiet', title: 'Đóng session này và mọi thứ nó đang chạy'}, 'Kết thúc session');
const closeBtn = h('button', {type: 'button', class: 'chat-btn chat-close', 'aria-label': 'Đóng khung bên'}, 'Đóng');

const itemsBox = h('div', {class: 'chat-items'});
const activityEl = h('p', {class: 'chat-activity', 'aria-hidden': 'true'});
const log = h('div', {class: 'chat-log', role: 'log', 'aria-live': 'polite'}, itemsBox, activityEl);
const pendingBox = h('div', {class: 'chat-pending'});
const problemEl = h('p', {class: 'chat-problem', role: 'alert'});

const overText = h('span', {class: 'chat-over-text'}, 'Session đã kết thúc.');
const againBtn = h('button', {type: 'button', class: 'chat-btn'}, 'Session mới');
const forgetBtn = h('button', {type: 'button', class: 'chat-btn chat-btn-quiet', title: 'Bỏ session đã kết thúc này khỏi danh sách của app. Transcript của Claude Code không bị xoá.'}, 'Bỏ khỏi danh sách');
const carryBtn = h('button', {
    type: 'button', class: 'chat-btn',
    title: 'Mở lại chính session này và giao việc tiếp',
}, 'Tiếp tục session này');
const overBar = h('div', {class: 'chat-over'}, overText, carryBtn, againBtn, forgetBtn);

const readonlyEl = h('p', {class: 'chat-readonly'});

// Under the steps of a session that has closed: the way to carry it on.
const closedText = h('span', {class: 'chat-over-text'}, 'Session này đã đóng.');
const resumeBtn = h('button', {
    type: 'button', class: 'chat-btn',
    title: 'Mở lại session này trong app và giao việc tiếp. Claude Code nạp lại toàn bộ hội thoại cũ.',
}, 'Tiếp tục session này');
const closedBar = h('div', {class: 'chat-over'}, closedText, resumeBtn);

const modeBox = h('fieldset', {class: 'chat-modes'}, h('legend', null, 'Quyền của agent trong session này'));
for (const m of MODES) {
    const radio = h('input', {type: 'radio', name: 'chat-mode', value: m.value, id: `chat-mode-${m.value}`});
    modeBox.append(h('label', {class: 'chat-mode', for: `chat-mode-${m.value}`},
        radio, h('span', {class: 'chat-mode-label'}, m.label), h('span', {class: 'chat-mode-hint'}, m.hint)));
}
// The model is the session's for its whole life; like the mode it is chosen
// anew for each session and starts from Claude Code's own default.
const modelPick = h('select', {class: 'chat-pick chat-model', id: 'chat-model'});
const modelRow = h('label', {class: 'chat-model-row', for: 'chat-model'}, h('span', {class: 'chat-model-label'}, 'Model'), modelPick);
const input = h('textarea', {class: 'chat-input', rows: 2, 'aria-label': 'Yêu cầu gửi cho agent'});
const sendBtn = h('button', {type: 'submit', class: 'chat-btn chat-send'}, 'Gửi');
const form = h('form', {class: 'chat-form'}, modeBox, modelRow, input, sendBtn);

const drawer = h('aside', {class: 'chat', 'aria-label': 'Khung làm việc với agent', hidden: true},
    // The title gets the full width of the first row; what acts on the
    // session sits on a row of its own below it.
    h('header', {class: 'chat-head'},
        h('div', {class: 'chat-heading'}, projectEl, titleEl, metaEl),
        h('div', {class: 'chat-actions'}, picker, stopBtn, endBtn),
        closeBtn,
    ),
    log,
    pendingBox,
    problemEl,
    overBar,
    readonlyEl,
    closedBar,
    form,
);

function chosenMode(): PermMode {
    return (modeBox.querySelector<HTMLInputElement>('input:checked')?.value as PermMode) ?? 'ask';
}

function chooseMode(mode: PermMode): void {
    for (const r of Array.from(modeBox.querySelectorAll<HTMLInputElement>('input'))) r.checked = r.value === mode;
}

function fail(err: unknown): void {
    problem = err instanceof Error ? err.message : String(err);
    draw();
}

// ---- what is shown in the log ------------------------------------------------

// Entry is one row of the log, whether it comes from a conversation of the
// app's own or from the trail of an agent that runs elsewhere.
interface Entry {
    key: string;
    // stamp changes when the row has to be drawn again.
    stamp: string;
    kind: 'user' | 'text' | 'tool' | 'notice' | 'error' | 'report';
    who: string;
    text: string;
    tool: string;
    line: string;
    input: string;
    // note is the agent's own description of a command: its claim, shown
    // apart from the command and never in its place.
    note?: string;
    change?: agent.Change;
    result: string;
    failed: boolean;
    status: { cls: string; label: string } | null;
    live: boolean;
    openFirst: boolean;
    at: number;
}

const HIDDEN = '(đang ẩn nội dung)';

// short shows a path inside the project relative to it: the part worth
// reading is the end, not the folders everything shares.
function short(path: string, dir: string): string {
    if (!dir) return path;
    const norm = (s: string) => s.replace(/\\/g, '/');
    const base = norm(dir).replace(/\/+$/, '');
    const full = norm(path);
    if (full.length > base.length + 1 && full.slice(0, base.length).toLowerCase() === base.toLowerCase() && full[base.length] === '/') {
        return full.slice(base.length + 1);
    }
    return path;
}

function firstLine(text: string): string {
    const line = text.split('\n').find(l => l.trim() !== '') ?? '';
    return line.length > 160 ? line.slice(0, 159) + '…' : line;
}

function toolStatus(it: agent.Item): { cls: string; label: string } {
    if (it.decision === 'denied') return {cls: 'is-denied', label: 'đã từ chối'};
    if (!it.done) return {cls: 'is-running', label: 'đang chạy'};
    if (it.isError) return {cls: 'is-error', label: 'lỗi'};
    return {cls: 'is-ok', label: 'xong'};
}

function chatEntries(): Entry[] {
    const dir = view?.dir ?? '';
    return Array.from(items.values()).sort((a, b) => a.seq - b.seq).map(it => {
        const base: Entry = {
            key: `c${it.seq}`, stamp: String(it.rev), kind: 'notice', who: '', text: it.text, tool: '', line: '',
            input: '', result: '', failed: false, status: null, live: false, openFirst: false, at: it.atMs,
        };
        switch (it.kind) {
            case 'user':
                return {...base, kind: 'user' as const, who: 'Bạn'};
            case 'text':
                return {...base, kind: 'text' as const, live: it.live};
            case 'tool':
                return {
                    ...base, kind: 'tool' as const, tool: it.tool,
                    line: it.change ? short(it.change.path, dir) : short(firstLine(it.input), dir),
                    input: it.input, change: it.change, result: it.result, failed: it.isError,
                    status: toolStatus(it),
                    // A change to a file is what the user most wants to see: it starts open.
                    openFirst: Boolean(it.change),
                };
            case 'error':
                return {...base, kind: 'error' as const};
            default:
                return base;
        }
    });
}

function trailEntries(): Entry[] {
    const seen = new Map<string, number>();
    const hide = Boolean(trail?.hidden);
    return (trail?.steps ?? []).map(st => {
        // A step is known by when it happened and what it was; the count
        // tells apart two alike in the same instant.
        const sig = `${st.atMs}:${st.kind}:${st.tool}`;
        const n = (seen.get(sig) ?? 0) + 1;
        seen.set(sig, n);
        const id = `${sig}:${n}`;
        if (!trailKeys.has(id)) trailKeys.set(id, trailKeys.size + 1);
        const base: Entry = {
            key: `t${trailKeys.get(id)}`,
            stamp: `${st.done}|${st.failed}|${st.detail.length}|${(st.note ?? '').length}|${st.result.length}|${hide}`,
            kind: 'text', who: '', text: hide ? HIDDEN : st.detail, tool: '', line: '', input: '', result: '',
            failed: false, status: null, live: false, openFirst: false, at: st.atMs,
        };
        switch (st.kind) {
            case 'prompt':
                return {...base, kind: 'user' as const, who: 'Yêu cầu'};
            case 'report':
                return {...base, kind: 'report' as const, who: 'Báo cáo cuối', text: hide ? HIDDEN : st.detail || 'Đã trả kết quả.'};
            case 'tool':
                return {
                    ...base, kind: 'tool' as const, text: '', tool: st.tool, line: hide ? '' : st.detail,
                    input: hide ? '' : st.detail, note: hide ? '' : st.note, result: st.result, failed: st.failed,
                    status: !st.done ? {cls: 'is-running', label: 'chưa có kết quả'}
                        : st.failed ? {cls: 'is-error', label: 'lỗi'} : {cls: 'is-ok', label: 'xong'},
                };
            default:
                return base;
        }
    });
}

// ---- drawing ------------------------------------------------------------------

// What the agent writes is Markdown, and is drawn as such: see markdown.ts.
// What the user typed is shown exactly as typed.

// changeNode draws an edit to a file as a diff. Every line of the change is
// there: nothing is folded away.
function changeNode(c: agent.Change, dir: string): HTMLElement {
    const body = h('div', {class: 'diff-body'});
    for (const l of c.lines ?? []) {
        if (l.op === '!') {
            // Not a line of the file: a remark about the change.
            body.append(h('div', {class: 'diff-line is-note'}, h('span', {class: 'diff-op', 'aria-hidden': 'true'}), h('span', {class: 'diff-text'}, l.text)));
            continue;
        }
        const cls = l.op === '+' ? 'is-added' : l.op === '-' ? 'is-removed' : 'is-same';
        body.append(h('div', {class: `diff-line ${cls}`},
            h('span', {class: 'diff-op', 'aria-hidden': 'true'}, l.op === '-' ? '−' : l.op),
            h('span', {class: 'vh'}, l.op === '+' ? 'thêm: ' : l.op === '-' ? 'bỏ: ' : ''),
            h('span', {class: 'diff-text'}, l.text)));
    }
    const added = (c.lines ?? []).filter(l => l.op === '+').length;
    const removed = (c.lines ?? []).filter(l => l.op === '-').length;
    return h('div', {class: 'diff'},
        h('p', {class: 'diff-head'},
            h('span', {class: 'diff-path', title: c.path}, short(c.path, dir)),
            h('span', {class: 'diff-count'},
                added > 0 && h('span', {class: 'diff-plus'}, `+${added}`),
                removed > 0 && h('span', {class: 'diff-minus'}, `−${removed}`)),
        ),
        c.note && h('p', {class: 'diff-note'}, c.note),
        body,
    );
}

function entryNode(e: Entry, dir: string): HTMLElement {
    switch (e.kind) {
        case 'user':
            // The blue edge says whose words these are; the label is for a
            // screen reader, or for a trail, where it is not the user's.
            return h('div', {class: 'msg msg-user'},
                h('span', {class: e.who === 'Bạn' ? 'vh' : 'msg-who'}, e.who), h('p', {class: 'msg-prose'}, e.text));
        case 'text':
            return h('div', {class: 'msg msg-agent' + (e.live ? ' is-live' : '')}, ...markdown(e.text));
        case 'report':
            return h('div', {class: 'msg msg-agent msg-report'}, h('span', {class: 'msg-who'}, e.who), ...markdown(e.text));
        case 'tool': {
            const details = h('details', {class: 'msg-details'},
                h('summary', null,
                    h('span', {class: 'tool'}, e.tool),
                    h('span', {class: 'msg-tool-line'}, e.line),
                    e.status && h('span', {class: `msg-tool-status ${e.status.cls}`}, e.status.label),
                ),
                e.change ? changeNode(e.change, dir) : e.input && h('pre', {class: 'msg-code'}, e.input),
                e.note && h('div', {class: 'msg-claim'},
                    h('span', {class: 'permit-label'}, 'Agent mô tả lệnh này là'),
                    h('p', {class: 'permit-claim'}, e.note)),
                e.result && h('pre', {class: 'msg-code msg-result' + (e.failed ? ' is-error' : '')}, e.result),
                !e.change && !e.input && !e.result && h('p', {class: 'msg-none'}, 'Không có gì thêm để xem.'),
            );
            details.open = toggled.get(e.key) ?? e.openFirst;
            return h('div', {class: 'msg msg-tool'}, details);
        }
        case 'error':
            return h('div', {class: 'msg msg-note is-error'}, e.text);
        default:
            return h('div', {class: 'msg msg-note'}, e.text);
    }
}

function drawLog(entries: Entry[], dir: string, emptyNote: Node | null): void {
    const have = new Map<string, HTMLElement>();
    for (const el of Array.from(itemsBox.children) as HTMLElement[]) have.set(el.dataset.key ?? '', el);

    // What is no longer wanted goes first, so the walk below only ever stands
    // on nodes that stay.
    const wanted = new Set(entries.map(e => e.key));
    for (const [key, el] of have) {
        if (!wanted.has(key)) {
            el.remove();
            have.delete(key);
        }
    }

    let cursor = itemsBox.firstElementChild;
    for (const e of entries) {
        let el = have.get(e.key);
        if (!el || el.dataset.stamp !== e.stamp) {
            const fresh = entryNode(e, dir);
            fresh.dataset.key = e.key;
            fresh.dataset.stamp = e.stamp;
            if (el) {
                // The node being replaced may be the one the walk stands on.
                if (cursor === el) cursor = fresh;
                el.replaceWith(fresh);
            }
            el = fresh;
        }
        if (el !== cursor) itemsBox.insertBefore(el, cursor);
        cursor = el.nextElementSibling;
    }
    if (entries.length === 0 && emptyNote) {
        const note = h('div', {class: 'chat-empty', 'data-key': 'empty', 'data-stamp': emptyNote.textContent ?? ''}, emptyNote);
        itemsBox.replaceChildren(note);
    }
}

function activityText(): string {
    if (showing !== 'chat' || !view) return '';
    switch (view.state) {
        case 'starting':
            return 'Đang khởi động Claude Code';
        case 'working':
            switch (view.activity) {
                case 'thinking':
                    return 'Đang suy nghĩ';
                case 'writing':
                    return view.activityTool ? `Đang chuẩn bị gọi ${view.activityTool}` : 'Đang viết';
                case 'tool':
                    return `Đang chạy ${view.activityTool || 'tool'}`;
                default:
                    return 'Đang làm';
            }
        default:
            return '';
    }
}

function drawActivity(): void {
    const text = activityText();
    activityEl.hidden = text === '';
    if (text === '') return;
    if (activityEl.dataset.text !== text) {
        activityEl.dataset.text = text;
        activityEl.replaceChildren(h('span', {class: 'chat-dots'}, h('i'), h('i'), h('i')), text);
    }
}

// ---- permission cards ---------------------------------------------------------

function permitCard(p: agent.Permission, dir: string): HTMLElement {
    const body = h('div', {class: 'permit-body', tabindex: 0, 'aria-label': 'Nội dung agent xin phép thực hiện'});
    if (p.change) {
        body.append(changeNode(p.change, dir));
    } else if ((p.fields ?? []).length === 0) {
        body.append(h('p', {class: 'permit-none'}, 'Tool này được gọi không kèm tham số nào.'));
    } else {
        for (const field of p.fields) {
            body.append(h('div', {class: 'permit-field' + (field.claim ? ' is-claim' : '')},
                h('span', {class: 'permit-label'}, field.label),
                field.claim
                    // What the agent says about its own action is its claim,
                    // set apart from the action itself.
                    ? h('p', {class: 'permit-claim'}, field.value)
                    : h('pre', {class: 'msg-code permit-value'}, field.value)));
        }
    }
    return h('div', {class: 'permit', 'data-request': p.requestId},
        h('p', {class: 'permit-title'}, 'Agent xin phép dùng ', h('span', {class: 'tool'}, p.tool)),
        body,
        // A cut must never pass for the end of what will run.
        p.hidden > 0 && h('p', {class: 'permit-warn'},
            `Nội dung quá dài: ${p.hidden.toLocaleString('vi-VN')} ký tự cuối không hiện ở đây. Hãy từ chối nếu bạn không chắc phần đó là gì.`),
        p.marked > 0 && h('p', {class: 'permit-warn'},
            `Có ${p.marked.toLocaleString('vi-VN')} ký tự không nhìn thấy được trong nội dung. Chúng đã được thay bằng dấu ␍ hoặc ⟨U+…⟩; hãy xem kỹ những chỗ đó.`),
        h('p', {class: 'permit-hint'}, 'Cuộn hết nội dung ở trên để bật nút Cho phép.'),
        h('div', {class: 'permit-actions'},
            h('button', {type: 'button', class: 'chat-btn permit-allow', 'data-permit': 'allow', disabled: true}, 'Cho phép'),
            h('button', {type: 'button', class: 'chat-btn', 'data-permit': 'deny', disabled: true}, 'Từ chối'),
        ),
    );
}

// armCard decides when a card's buttons may be pressed. "Cho phép" needs two
// things: the card has been still for a moment, so a click aimed elsewhere
// cannot land on it, and all of the input has been in view, so nothing below
// the fold is allowed unread.
function armCard(card: HTMLElement): void {
    const id = card.dataset.request ?? '';
    const body = card.querySelector<HTMLElement>('.permit-body')!;
    const hint = card.querySelector<HTMLElement>('.permit-hint')!;
    const allow = card.querySelector<HTMLButtonElement>('[data-permit="allow"]')!;
    const deny = card.querySelector<HTMLButtonElement>('[data-permit="deny"]')!;
    const seenAll = () => body.scrollHeight - body.scrollTop - body.clientHeight < 6;
    if (seenAll()) card.dataset.seen = '1';
    const busy = answering.has(id);
    const armed = card.dataset.armed === '1';
    allow.disabled = busy || !armed || card.dataset.seen !== '1';
    deny.disabled = busy || !armed;
    hint.hidden = card.dataset.seen === '1';
}

function rearm(card: HTMLElement): void {
    card.dataset.armed = '';
    const stamp = String(Date.now());
    card.dataset.armStamp = stamp;
    armCard(card);
    // The wait is counted from when the card has been drawn, not from when
    // it was made: if the page was busy in between, a wait counted from the
    // making could be over before the card was ever on screen. Two frames,
    // because the first callback runs before the frame that paints it.
    requestAnimationFrame(() => requestAnimationFrame(() => {
        window.setTimeout(() => {
            if (card.dataset.armStamp !== stamp || !card.isConnected) return;
            card.dataset.armed = '1';
            armCard(card);
        }, ARM_MS);
    }));
}

function drawPending(): void {
    const wanted = showing === 'chat' ? view?.pending ?? [] : [];
    const have = new Map<string, HTMLElement>();
    for (const el of Array.from(pendingBox.children) as HTMLElement[]) have.set(el.dataset.request ?? '', el);

    let moved = false;
    const ids = new Set(wanted.map(p => p.requestId));
    for (const [id, el] of have) {
        if (!ids.has(id)) {
            el.remove();
            have.delete(id);
            answering.delete(id);
            moved = true;
        }
    }
    for (const p of wanted) {
        let card = have.get(p.requestId);
        if (!card) {
            card = permitCard(p, view?.dir ?? '');
            card.querySelector('.permit-body')!.addEventListener('scroll', () => armCard(card!), {passive: true});
            pendingBox.append(card);
            rearm(card);
        } else {
            armCard(card);
        }
    }
    // A card that disappears pulls the next one up under the pointer: all of
    // them go quiet for a moment.
    if (moved) for (const card of Array.from(pendingBox.children) as HTMLElement[]) rearm(card);
}

// ---- the heading ----------------------------------------------------------------

// closedOnBoard finds a session among those the board lists as closed today.
function closedOnBoard(id: string): { session: monitor.ClosedSession; project: monitor.Project } | null {
    for (const project of board?.projects ?? []) {
        for (const session of (project as monitor.Project & { closed?: monitor.ClosedSession[] }).closed ?? []) {
            if (session.id === id) return {session, project};
        }
    }
    return null;
}

function sessionOnBoard(id: string): { session: monitor.Session; project: monitor.Project } | null {
    for (const project of board?.projects ?? []) {
        for (const session of project.sessions) {
            if (session.id === id) return {session, project};
        }
    }
    return null;
}

function projectOfDir(dir: string): monitor.Project | null {
    return board?.projects.find(p => p.path === dir) ?? null;
}

function baseName(dir: string): string {
    return dir.split(/[\\/]/).filter(Boolean).pop() ?? dir;
}

function drawHeading(): void {
    if (showing === 'draft' && draft) {
        projectEl.textContent = draft.projectName;
        if (draft.resume) {
            titleEl.textContent = draft.resume.title || 'Session đã đóng';
            metaEl.replaceChildren('Tiếp tục session đã đóng. Nó mở lại khi bạn gửi yêu cầu.');
            return;
        }
        titleEl.textContent = 'Session mới';
        metaEl.replaceChildren('Chưa mở. Session bắt đầu khi bạn gửi yêu cầu đầu tiên.');
        return;
    }
    if (showing === 'trail' && target) {
        const found = sessionOnBoard(target.sessionId);
        const a = found?.session.agents.find(x => x.id === target!.agentId);
        const closed = found ? null : closedOnBoard(target.sessionId);
        projectEl.textContent = found?.project.name ?? closed?.project.name ?? '';
        // The steps of a session that runs elsewhere follow "Ẩn nội dung", and
        // so does the heading above them: the board's rule, see render.ts.
        const label = `Session ${target.sessionId.slice(0, 8)}`;
        if (closed) {
            const c = closed.session;
            titleEl.textContent = privacy ? label : c.title || 'Session chưa có tiêu đề';
            metaEl.replaceChildren(
                statusNode({cls: 'is-done', icon: 'square', label: 'Đã đóng'}),
                ' · ' + [c.modelLabel, f.usd(c.all.costUsd), f.ago(c.endedAtMs, board?.generatedAtMs ?? Date.now())].filter(Boolean).join(' · '));
        } else if (!found) {
            titleEl.textContent = 'Session đã đóng';
            metaEl.replaceChildren('Session này không còn trên bảng.');
        } else if (target.agentId === '') {
            const s = found.session;
            titleEl.textContent = privacy ? label : s.title || s.name || 'Luồng chính';
            metaEl.replaceChildren(statusNode(sessionStatus(s)), [' · luồng chính', s.modelLabel].filter(Boolean).join(' · '));
        } else if (a) {
            titleEl.textContent = privacy ? a.type || 'Agent' : a.description || a.type || 'Agent';
            metaEl.replaceChildren(statusNode(agentStatus(a)),
                ' · ' + [!privacy && a.type !== a.description ? a.type : '', a.modelLabel, `${a.toolUses} lần gọi tool`].filter(Boolean).join(' · '));
        } else {
            titleEl.textContent = 'Agent';
            metaEl.replaceChildren('Không còn thấy agent này trong session.');
        }
        return;
    }
    const state = view?.state ?? '';
    const onBoard = sessionOnBoard(currentId);
    projectEl.textContent = onBoard?.project.name ?? (view ? baseName(view.dir) : '');
    // The board names a session by the title Claude Code gives it; until it
    // has one, the first request stands in.
    titleEl.textContent = onBoard?.session.title || view?.title || 'Session';
    metaEl.replaceChildren(
        h('span', {class: `chat-state is-${state}`}, STATE[state] ? statusNode(STATE[state]) : state),
        view?.model ? ` · ${view.model}` : '',
        view && view.costUsd > 0 ? ` · ${f.usd(view.costUsd)}` : '',
    );
}

function draftNote(): Node {
    const where = draft ? projectOfDir(draft.projectPath)?.path ?? draft.projectPath : '';
    const box = h('div', {class: 'chat-brief'},
        h('p', null, draft?.resume ? 'Session sẽ mở lại tại' : 'Session mới sẽ chạy tại'),
        h('p', {class: 'chat-brief-path'}, where),
    );
    if (draft?.resume) {
        box.append(h('p', {class: 'chat-brief-with'},
            'Claude Code nạp lại toàn bộ hội thoại cũ, nên lượt đầu tốn hơn một lượt thường. Các bước trước đó xem ở dòng của session trên bảng.'));
    }
    if (claude?.problem) box.append(h('p', {class: 'chat-brief-warn'}, claude.problem));
    else if (claude?.path) box.append(h('p', {class: 'chat-brief-with', title: claude.path}, 'bằng Claude Code ở ', h('span', {class: 'chat-brief-exe'}, claude.path)));
    return box;
}

function draw(): void {
    drawer.hidden = showing === 'none';
    document.body.classList.toggle('has-chat', showing !== 'none');
    drawer.dataset.showing = showing;
    if (showing === 'none') return;

    // Whether the reader is at the end is asked before anything changes
    // height, and acted on after everything has.
    const nearBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 80;

    const state = view?.state ?? '';
    const over = showing === 'chat' && (state === 'ended' || state === 'failed');
    const live = showing === 'chat' && view !== null && !over;
    drawHeading();

    stopBtn.hidden = !(live && (state === 'working' || state === 'waiting' || state === 'starting'));
    endBtn.hidden = !live;
    form.hidden = showing === 'trail' || over;
    modeBox.hidden = showing !== 'draft';
    modelRow.hidden = showing !== 'draft';
    overBar.hidden = !over;
    readonlyEl.hidden = showing !== 'trail';
    // Only the main thread of a closed session can be carried on, and only
    // while the board still lists it.
    const closedTrail = showing === 'trail' && Boolean(trail?.closed) && target?.agentId === '' && closedOnBoard(target.sessionId) !== null;
    closedBar.hidden = !closedTrail;
    resumeBtn.disabled = resuming;
    carryBtn.disabled = resuming;
    if (showing === 'draft') drawModels();
    sendBtn.disabled = sending;
    input.placeholder = showing === 'draft' && !draft?.resume
        ? 'Nhập yêu cầu đầu tiên. Enter để gửi, Shift+Enter để xuống dòng.'
        : 'Nhập yêu cầu tiếp theo. Enter để gửi, Shift+Enter để xuống dòng.';

    if (over && view) {
        const project = projectOfDir(view.dir);
        overText.textContent = state === 'failed' ? 'Session đã dừng do lỗi.' : 'Session đã kết thúc.';
        againBtn.hidden = project === null;
        againBtn.textContent = project ? `Session mới ở ${project.name}` : 'Session mới';
        // It can be carried on once the board has noticed that it closed.
        carryBtn.hidden = closedOnBoard(currentId) === null;
    }

    let shownProblem = problem;
    if (showing === 'chat') shownProblem = problem || view?.problem || '';
    if (showing === 'trail') shownProblem = trailProblem;
    problemEl.textContent = shownProblem;
    problemEl.hidden = shownProblem === '';

    if (showing === 'trail') {
        readonlyEl.textContent = trail?.hidden
            ? 'Chỉ xem. Nội dung đang được ẩn theo công tắc "Ẩn nội dung".'
            : trail?.closed
                ? 'Chỉ xem: đây là những bước cuối của một session đã đóng. Câu chữ dài được cắt bớt, chuỗi trông như mật khẩu được che.'
                : 'Chỉ xem. Session này chạy ngoài app nên không ra lệnh được từ đây. Câu chữ dài được cắt bớt, chuỗi trông như mật khẩu được che.';
        const entries = trailEntries();
        const earlier: Entry[] = trail?.earlier && entries.length > 0
            ? [{
                key: 'earlier', stamp: '1', kind: 'notice', who: '', text: 'Các bước trước đó không hiện ở đây.', tool: '',
                line: '', input: '', result: '', failed: false, status: null, live: false, openFirst: false, at: 0,
            }]
            : [];
        drawLog([...earlier, ...entries], '', trail ? document.createTextNode('Agent này chưa ghi bước nào.') : document.createTextNode('Đang đọc các bước…'));
    } else if (showing === 'draft') {
        drawLog([], '', draftNote());
    } else {
        drawLog(chatEntries(), view?.dir ?? '', document.createTextNode(view ? 'Chưa có nội dung.' : 'Đang mở…'));
    }
    drawActivity();
    drawPending();
    if (nearBottom) log.scrollTop = log.scrollHeight;
}

// drawModels lists the models a session can be started with. The first entry
// leaves the choice to Claude Code's own settings.
function drawModels(): void {
    const rows = [
        {value: '', label: 'Mặc định của Claude Code'},
        ...models.map(m => ({value: m.id, label: `${m.label} · mở session khoảng ${f.usd(m.openUsd)}`})),
    ];
    const shape = JSON.stringify(rows);
    if (shape === modelShape) return;
    modelShape = shape;
    const chosen = modelPick.value;
    modelPick.replaceChildren(...rows.map(r => h('option', {value: r.value}, r.label)));
    modelPick.value = rows.some(r => r.value === chosen) ? chosen : '';
    modelPick.title = 'Model của session này. Con số là chi phí nạp system prompt khi mở session, quy theo giá API.';
}

async function drawPicker(): Promise<void> {
    if (showing === 'none') return;
    const ask = ++pickerAsk;
    let list: agent.View[] = [];
    try {
        list = await ListChats();
    } catch {
        return;
    }
    // An older answer must not overwrite a newer one.
    if (ask !== pickerAsk) return;

    // While talking to the only session there is, there is nothing to pick.
    const others = list.some(v => v.id !== currentId);
    const want = list.length > 0 && (others || showing !== 'chat');
    const rows = list.map(v => ({value: v.id, label: `${baseName(v.dir)} · ${v.title}`}));
    const here = showing === 'chat' && list.some(v => v.id === currentId) ? currentId : '';
    if (here === '') rows.unshift({value: '', label: 'Chuyển sang session của app…'});
    const shape = JSON.stringify([want, here, rows]);
    if (shape === pickerShape) return;
    pickerShape = shape;

    picker.hidden = !want;
    picker.replaceChildren(...rows.map(r => {
        const o = h('option', {value: r.value}, r.label);
        o.selected = r.value === here;
        return o;
    }));
}

// ---- data ---------------------------------------------------------------------

async function pull(): Promise<void> {
    if (showing !== 'chat' || !currentId) return;
    if (fetching) {
        again = true;
        return;
    }
    fetching = true;
    const id = currentId;
    const asked = gen;
    try {
        const res = await GetChat(id, rev);
        if (asked !== gen) return;
        if (res.problem) {
            problem = res.problem;
        } else {
            for (const it of res.view.items ?? []) items.set(it.seq, it);
            // The app keeps only the latest items; what it has dropped is
            // dropped here too.
            for (const seq of Array.from(items.keys())) {
                if (seq < res.view.firstSeq) items.delete(seq);
            }
            rev = res.view.rev;
            view = res.view;
        }
        draw();
    } catch (err) {
        if (asked === gen) fail(err);
    } finally {
        fetching = false;
        if (again) {
            again = false;
            void pull();
        }
    }
}

async function pullTrail(): Promise<void> {
    if (showing !== 'trail' || !target) return;
    const asked = gen;
    try {
        const res = await GetTrail(target.sessionId, target.agentId);
        if (asked !== gen) return;
        trailProblem = res.problem;
        // A session that has gone keeps the steps already read on screen.
        if (!res.problem || !trail) trail = res.trail;
        draw();
    } catch (err) {
        if (asked !== gen) return;
        trailProblem = err instanceof Error ? err.message : String(err);
        draw();
    }
}

// Words typed and not sent belong to where they were going: one session, or
// the draft of a new one in one project. The box is shared, so they are put
// away when the panel turns elsewhere and given back when it returns; they
// must never follow the user into another session and be sent there.
const unsent = new Map<string, string>();

function boxKey(): string {
    if (showing === 'chat' && currentId) return `chat:${currentId}`;
    if (showing === 'draft' && draft) return draft.resume ? `resume:${draft.resume.sessionId}` : `new:${draft.projectPath}`;
    return '';
}

function putAway(): void {
    const key = boxKey();
    if (!key) return;
    if (input.value.trim() !== '') unsent.set(key, input.value);
    else unsent.delete(key);
}

function giveBack(): void {
    input.value = unsent.get(boxKey()) ?? '';
}

// forget drops words put away for a box once they have been sent after all.
function forget(key: string, sent: string): void {
    if (unsent.get(key)?.trim() === sent) unsent.delete(key);
}

// turn points the panel at something else and forgets what it held.
function turn(to: Showing): void {
    putAway();
    input.value = '';
    gen++;
    showing = to;
    currentId = '';
    draft = null;
    target = null;
    trail = null;
    trailProblem = '';
    view = null;
    items = new Map();
    rev = 0;
    problem = '';
    sending = false;
    resuming = false;
    toggled.clear();
    answering.clear();
    trailKeys = new Map();
    itemsBox.replaceChildren();
    pendingBox.replaceChildren();
    pickerShape = '';
}

function show(id: string): void {
    turn('chat');
    currentId = id;
    giveBack();
    draw();
    void pull();
    void drawPicker();
    onChange();
}

// ---- actions ------------------------------------------------------------------

async function submit(): Promise<void> {
    const text = input.value.trim();
    // One send at a time: a second Enter while the first is on its way would
    // start a second session, or send the request twice.
    if (!text || sending || (showing !== 'draft' && showing !== 'chat')) return;
    problem = '';
    sending = true;
    draw();
    const asked = gen;
    try {
        if (showing === 'draft' && draft) {
            const box = boxKey();
            const res = draft.resume
                ? await ResumeSession(draft.resume.sessionId, text, chosenMode(), modelPick.value)
                : await StartSession(draft.projectPath, text, chosenMode(), modelPick.value);
            if (asked !== gen) {
                // The panel turned elsewhere while the words were on their
                // way, and put them away. Once sent they must not be offered
                // again.
                if (res.id && !res.problem) forget(box, text);
                return;
            }
            if (!res.id) {
                problem = res.problem || 'Không mở được session.';
                return;
            }
            // With a problem the session exists but did not get the request:
            // the words go with it, to be sent again from its own box. The
            // draft's box is done with either way.
            const words = res.problem ? input.value : '';
            input.value = '';
            show(res.id);
            if (words) input.value = words;
            problem = res.problem;
        } else if (showing === 'chat' && currentId) {
            const box = boxKey();
            const err = await SendChat(currentId, text);
            if (asked !== gen) {
                if (!err) forget(box, text);
                return;
            }
            if (err) {
                problem = err;
                return;
            }
            input.value = '';
            void pull();
        }
    } catch (err) {
        if (asked === gen) problem = err instanceof Error ? err.message : String(err);
    } finally {
        if (asked === gen || showing === 'chat') {
            sending = false;
            draw();
            input.focus();
        }
    }
}

form.addEventListener('submit', event => {
    event.preventDefault();
    void submit();
});

input.addEventListener('keydown', event => {
    if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
        event.preventDefault();
        void submit();
    }
});

pendingBox.addEventListener('click', event => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>('[data-permit]');
    const card = button?.closest<HTMLElement>('[data-request]');
    if (!button || button.disabled || !card || !currentId) return;
    const id = card.dataset.request!;
    if (answering.has(id)) return;
    const asked = gen;
    answering.add(id);
    armCard(card);
    AnswerPermission(currentId, id, button.dataset.permit === 'allow').then(err => {
        if (asked !== gen) return;
        if (err) {
            // Not answered: the card can be used again.
            answering.delete(id);
            problem = err;
            draw();
            return;
        }
        problem = '';
        void pull();
    }, err => {
        if (asked !== gen) return;
        answering.delete(id);
        fail(err);
    });
});

// A tool call the user opened or closed stays that way when it is redrawn.
itemsBox.addEventListener('toggle', event => {
    const details = event.target as HTMLDetailsElement;
    const key = details.closest<HTMLElement>('[data-key]')?.dataset.key;
    if (key) toggled.set(key, details.open);
}, true);

stopBtn.addEventListener('click', () => {
    if (showing !== 'chat' || !currentId) return;
    const asked = gen;
    InterruptSession(currentId).then(err => {
        if (asked !== gen) return;
        problem = err;
        draw();
    }, err => {
        if (asked === gen) fail(err);
    });
});

endBtn.addEventListener('click', () => {
    if (showing !== 'chat' || !currentId) return;
    const asked = gen;
    endBtn.disabled = true;
    EndSession(currentId).then(err => {
        if (asked !== gen) return;
        problem = err;
        void pull();
    }, err => {
        if (asked === gen) fail(err);
    }).finally(() => {
        endBtn.disabled = false;
    });
});

forgetBtn.addEventListener('click', () => {
    if (showing !== 'chat' || !currentId) return;
    const asked = gen;
    ForgetSession(currentId).then(err => {
        if (asked !== gen) return;
        if (err) {
            problem = err;
            draw();
            return;
        }
        close();
    }, err => {
        if (asked === gen) fail(err);
    });
});

againBtn.addEventListener('click', () => {
    const project = view ? projectOfDir(view.dir) : null;
    if (project) openDraft(project.path, project.name);
});

// carryOn opens the draft that carries on a closed session, once its folder
// has been agreed to: the same question a new session there would raise.
function carryOn(sessionId: string): void {
    const found = closedOnBoard(sessionId);
    if (!found) {
        problem = 'Session này chưa hiện trong danh sách đã đóng trên bảng. Thử lại sau vài giây.';
        if (showing === 'trail') trailProblem = problem;
        draw();
        return;
    }
    if (resuming) return;
    resuming = true;
    draw();
    const asked = gen;
    const said = (text: string) => {
        if (asked !== gen) return;
        resuming = false;
        problem = text;
        if (showing === 'trail') trailProblem = text;
        draw();
    };
    TrustResume(sessionId).then(res => {
        if (asked !== gen) return;
        if (!res.ok) {
            said(res.problem);
            return;
        }
        openDraft(found.project.path, found.project.name, {sessionId, title: found.session.title});
    }, err => said(err instanceof Error ? err.message : String(err)));
}

resumeBtn.addEventListener('click', () => {
    if (showing === 'trail' && target && target.agentId === '') carryOn(target.sessionId);
});

carryBtn.addEventListener('click', () => {
    if (showing === 'chat' && currentId) carryOn(currentId);
});

closeBtn.addEventListener('click', () => close());

picker.addEventListener('change', () => {
    if (picker.value) show(picker.value);
});

drawer.addEventListener('keydown', event => {
    if (event.key === 'Escape') close();
});

// ---- what the rest of the page uses --------------------------------------------

export function close(): void {
    turn('none');
    draw();
    onChange();
}

/** Opens the panel on a session this app started. */
export function open(id: string): void {
    // Already there: nothing is thrown away, the box just gets the cursor.
    if (!(showing === 'chat' && currentId === id)) show(id);
    input.focus();
}

/**
 * Opens the panel ready to start a new session in a project, or, with resume,
 * to carry on one that has closed there.
 */
export function openDraft(projectPath: string, projectName: string, resume?: Draft['resume']): void {
    turn('draft');
    draft = {projectPath, projectName, resume};
    giveBack();
    // Every new session starts from the mode that asks first; a looser mode
    // is chosen each time, never carried over from the last session. The
    // model, likewise, goes back to Claude Code's own.
    chooseMode('ask');
    modelPick.value = '';
    draw();
    void drawPicker();
    input.focus();
    onChange();
    const asked = gen;
    GetClaude().then(info => {
        if (asked !== gen) return;
        claude = info;
        draw();
    }, () => { /* the note is a courtesy; starting will say what is wrong */ });
    GetModels().then(list => {
        models = list ?? [];
        if (asked === gen) draw();
    }, () => { /* without the list the session starts on Claude Code's default */ });
}

/** Opens the panel on the steps of a session or agent that runs elsewhere. */
export function openTrail(sessionId: string, agentId: string): void {
    if (showing === 'trail' && target?.sessionId === sessionId && target.agentId === agentId) {
        close();
        return;
    }
    turn('trail');
    target = {sessionId, agentId};
    draw();
    void pullTrail();
    void drawPicker();
    onChange();
}

/** Tells the panel about the board's latest snapshot. */
/** Tells the panel about the "Ẩn nội dung" switch. */
export function setPrivacy(on: boolean): void {
    if (privacy === on) return;
    privacy = on;
    if (showing === 'trail') draw();
}

export function sync(snap: monitor.Snapshot): void {
    board = snap;
    if (showing === 'none') return;
    if (showing === 'trail') {
        void pullTrail();
        return;
    }
    draw();
}

/** What the panel is showing, for the board to mark. */
export function current(): OpenMark {
    if (showing === 'draft' && draft) return {sessionId: '', agentId: '', projectPath: draft.projectPath};
    if (showing === 'trail' && target) {
        return {sessionId: target.sessionId, agentId: target.agentId, projectPath: sessionOnBoard(target.sessionId)?.project.path ?? ''};
    }
    if (showing === 'chat' && currentId) {
        const path = sessionOnBoard(currentId)?.project.path ?? (view ? projectOfDir(view.dir)?.path ?? '' : '');
        return {sessionId: currentId, agentId: '', projectPath: path};
    }
    return {sessionId: '', agentId: '', projectPath: ''};
}

/** Mounts the panel and starts listening for changes. */
export function mount(parent: HTMLElement, changed: () => void): void {
    onChange = changed;
    parent.append(drawer);
    EventsOn('chat', (id: string) => {
        if (showing === 'chat' && id === currentId) void pull();
        void drawPicker();
    });
    // A card whose input came to fit, or stopped fitting, is judged again.
    window.addEventListener('resize', () => {
        for (const card of Array.from(pendingBox.children) as HTMLElement[]) armCard(card);
    });
}
