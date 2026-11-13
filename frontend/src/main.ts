// Fonts are bundled into the app, so nothing is fetched at run time. Each
// package maps "<subset>-<weight>" to its stylesheet; only the Latin and
// Vietnamese subsets are needed.
import '@fontsource/be-vietnam-pro/latin-400';
import '@fontsource/be-vietnam-pro/vietnamese-400';
import '@fontsource/be-vietnam-pro/latin-500';
import '@fontsource/be-vietnam-pro/vietnamese-500';
import '@fontsource/be-vietnam-pro/latin-600';
import '@fontsource/be-vietnam-pro/vietnamese-600';
import '@fontsource/big-shoulders-display/latin-600';
import '@fontsource/big-shoulders-display/vietnamese-600';
import '@fontsource/big-shoulders-display/latin-700';
import '@fontsource/big-shoulders-display/vietnamese-700';
import '@fontsource/ibm-plex-mono/latin-400';
import '@fontsource/ibm-plex-mono/vietnamese-400';
import '@fontsource/ibm-plex-mono/latin-500';
import './style.css';

import {
    AddProject, ExportHistory, GetHistory, GetSnapshot, OpenCredit, OpenProject, RemoveProject, SetBudget, SetOptions, TrustProject,
} from '../wailsjs/go/main/App';
import {monitor} from '../wailsjs/go/models';
import {EventsOn} from '../wailsjs/runtime/runtime';
import * as chat from './chat';
import * as f from './format';
import * as past from './history';
import * as notices from './notices';
import {bays, h, lead, ledger, meter, nothingRunning, ViewState} from './render';
import {floor} from './scene';

// The Go side says when a new scan is ready, so the page does not keep a
// clock of its own. This one is the net under it: if an event is ever missed
// the page still catches up.
const FALLBACK_MS = 10_000;
// How often the history is fetched again: while it is on screen, while only
// its two-week strip is, and while old transcripts are still being read.
const HISTORY_MS = 15_000;
const HISTORY_IDLE_MS = 60_000;
const HISTORY_SCAN_MS = 2_000;
// How long after the chart is drawn as new its columns are left to rise.
const RISE_MS = 900;
// How many days the strip in the board's till shows.
const TREND_DAYS = 14;

// ---- remembered switches ------------------------------------------------------

type SwitchName = 'hideEnded' | 'privacy' | 'notify' | 'ledgerOpen';
const DEFAULTS: Record<SwitchName, boolean> = {hideEnded: false, privacy: false, notify: true, ledgerOpen: false};

function recall(name: SwitchName): boolean {
    try {
        const stored = localStorage.getItem(`monitor-agent.${name}`);
        return stored === null ? DEFAULTS[name] : stored === '1';
    } catch {
        return DEFAULTS[name];
    }
}

function remember(name: SwitchName, on: boolean): void {
    try {
        localStorage.setItem(`monitor-agent.${name}`, on ? '1' : '0');
    } catch {
        // The preference is a convenience; the page works without storage.
    }
}

function recallText(name: string): string {
    try {
        return localStorage.getItem(`monitor-agent.${name}`) ?? '';
    } catch {
        return '';
    }
}

function rememberText(name: string, value: string): void {
    try {
        localStorage.setItem(`monitor-agent.${name}`, value);
    } catch {
        // As above: a convenience.
    }
}

// The projects taken off the board, by path. A list someone else wrote into
// the store is not trusted to be a list.
function recallHidden(): Set<string> {
    try {
        const got: unknown = JSON.parse(recallText('hidden') || '[]');
        return new Set(Array.isArray(got) ? got.filter((x): x is string => typeof x === 'string').slice(0, 500) : []);
    } catch {
        return new Set();
    }
}

function rememberHidden(): void {
    rememberText('hidden', JSON.stringify(Array.from(view.hidden)));
}

function recallBudget(): number {
    const n = Number(recallText('budget'));
    return Number.isFinite(n) && n > 0 && n <= 1_000_000 ? n : 0;
}

const view: ViewState = {
    hideEnded: recall('hideEnded'),
    privacy: recall('privacy'),
    expanded: new Set(),
    ledgerOpen: recall('ledgerOpen'),
    open: {sessionId: '', agentId: '', projectPath: ''},
    filter: '',
    budget: recallBudget(),
    hidden: recallHidden(),
    showHidden: false,
};
chat.setPrivacy(view.privacy);

// Which of the two pages is on screen: the board of what runs now, or the
// history of what the days cost.
type Mode = 'board' | 'history';
let mode: Mode = 'board';
let history: monitor.History | null = null;
let historyProblem = '';
let historyAt = 0;
let historyLoading = false;
let riseUntil = 0;
const pastState: past.HistState = {
    range: (past.RANGES as number[]).includes(Number(recallText('histRange'))) ? Number(recallText('histRange')) as past.Range : 30,
    split: (['project', 'model', 'token'] as string[]).includes(recallText('histSplit')) ? recallText('histSplit') as past.Split : 'project',
    day: '',
    fresh: false,
    tableOpen: false,
};
// Where a clicked notification pointed, until the board has been drawn and
// can be scrolled there.
let pendingProject = '';
let notify = recall('notify');
let snapshot: monitor.Snapshot | null = null;
let failure = '';
let optionsProblem = '';
// What the last action on a project could not do. It is kept apart from the
// switches' problem: one must not wipe out the other.
let actionProblem = '';
let timer: number | undefined;
let loading = false;
// Set when a scan was announced while one was being fetched.
let stale = false;

// ---- the shell: built once ----------------------------------------------------

const root = document.querySelector<HTMLDivElement>('#app')!;
const stamp = h('span', {class: 'stamp'}, 'Đang đọc transcript…');
const content = h('main', {class: 'content'});

function toggle(id: string, label: string, title: string, on: boolean, change: (on: boolean) => void): HTMLElement {
    const box = h('input', {type: 'checkbox', id});
    box.checked = on;
    box.addEventListener('change', () => change(box.checked));
    return h('label', {class: 'switch', for: id, title}, box, label);
}

const switches = h('div', {class: 'switches'},
    toggle('sw-notify', 'Báo khi xong',
        'Báo khi một session xong lượt, khi nó dừng lại chờ bạn trả lời, và khi một agent làm xong: trong cửa sổ này, bằng nút trên taskbar, và bằng thông báo của Windows nếu Windows đang cho hiện',
        notify, on => {
            notify = on;
            remember('notify', on);
            void pushOptions(on);
        }),
    toggle('sw-ended', 'Ẩn agent đã kết thúc',
        'Chỉ để lại các agent đang chạy hoặc im lặng',
        view.hideEnded, on => {
            view.hideEnded = on;
            remember('hideEnded', on);
            render();
        }),
    toggle('sw-privacy', 'Ẩn nội dung',
        'Giấu câu lệnh, yêu cầu, kết quả, tiêu đề session, mô tả agent và tên nhánh, dùng khi chia sẻ màn hình. Tên project, số liệu và tên tool vẫn hiện. Khung chat của session app mở không bị che.',
        view.privacy, on => {
            view.privacy = on;
            remember('privacy', on);
            chat.setPrivacy(on);
            void pushOptions(false);
            render();
        }),
);

function tab(to: Mode, label: string, title: string): HTMLButtonElement {
    return h('button', {type: 'button', class: 'view-tab', 'data-view': to, title}, label);
}

const tabs = h('nav', {class: 'views', 'aria-label': 'Trang'},
    tab('board', 'Bàn làm việc', 'Những gì đang chạy và chi phí của hôm nay'),
    tab('history', 'Lịch sử', 'Chi phí từng ngày, theo project và model'),
);

// The filter narrows the board to the rows that carry what is typed. It sits
// in the bar, outside what is redrawn, so typing is never interrupted.
const find = h('input', {
    type: 'search', class: 'find', placeholder: 'Lọc project, session, agent', spellcheck: 'false',
    'aria-label': 'Lọc bảng theo tên project, session hoặc agent', title: 'Phím tắt: /',
});
find.addEventListener('input', () => {
    view.filter = find.value;
    render();
});
find.addEventListener('keydown', event => {
    if (event.key !== 'Escape') return;
    // The first Esc empties the box; with nothing in it, the box is left.
    if (find.value !== '') {
        find.value = '';
        view.filter = '';
        render();
    } else {
        find.blur();
    }
    event.stopPropagation();
});

const topBar = h('header', {class: 'top'},
    h('div', {class: 'top-left'}, h('span', {class: 'brand'}, 'Agent Desk'), tabs),
    h('div', {class: 'top-right'}, find, notices.bell, switches, stamp),
);

// The readout of one day of the chart. It follows the pointer and the
// keyboard, and lives outside what is redrawn.
const tip = h('div', {class: 'tip', role: 'tooltip', hidden: true});
let tipDay = '';
// The credit line at the foot of the page. Each link names an address the Go
// side holds; the page passes the name, never an address.
const credit = h('footer', {class: 'credit'},
    h('span', null, '© 2026 Khoa Nguyen'),
    h('button', {
        type: 'button', class: 'credit-link', 'data-credit': 'site',
        title: 'Mở https://khoabug.com/ trong trình duyệt',
    }, 'khoabug.com'),
    h('button', {
        type: 'button', class: 'credit-link', 'data-credit': 'profile',
        title: 'Mở https://www.linkedin.com/in/khoanguyenit/ trong trình duyệt',
    }, 'LinkedIn'),
);
root.append(topBar, content, credit, tip);

// The board's heading row sticks just under the top bar, and a desk scrolls
// its project clear of both. The bar wraps onto two lines in a narrow window,
// so its real height is handed to the stylesheet.
new ResizeObserver(() => {
    document.documentElement.style.setProperty('--top-h', `${Math.ceil(topBar.getBoundingClientRect().height)}px`);
}).observe(topBar);

// pushOptions tells the Go side about the switches it acts on. announce asks
// for a confirmation notification, sent when the user has just turned
// notifications on, so they can see that notifications reach them.
async function pushOptions(announce: boolean): Promise<void> {
    try {
        optionsProblem = await SetOptions(notify, view.privacy, announce);
    } catch (err) {
        optionsProblem = err instanceof Error ? err.message : String(err);
    }
    render();
}

// ---- rendering ----------------------------------------------------------------

// patch makes parent's children match next's while keeping every keyed child
// whose markup did not change. A kept node keeps its tooltip, its text
// selection and its focus, which a wholesale replace would drop every refresh.
// Children marked as containers are patched one level down instead of replaced.
function patch(parent: Element, next: Element): void {
    const current = new Map<string, Element>();
    for (const el of Array.from(parent.children)) {
        const key = (el as HTMLElement).dataset.key;
        if (key) current.set(key, el);
    }

    const wanted: Element[] = [];
    for (const el of Array.from(next.children)) {
        const key = (el as HTMLElement).dataset.key;
        const old = key ? current.get(key) : undefined;
        if (old && old.outerHTML === el.outerHTML) {
            wanted.push(old);
        } else if (old && (el as HTMLElement).dataset.container !== undefined && old.tagName === el.tagName) {
            patch(old, el);
            wanted.push(old);
        } else if (old) {
            // Swap in place, in one step. Inserting the new node first and
            // removing the old one later would push the page down for a
            // moment, and the browser would scroll to compensate.
            old.replaceWith(el);
            wanted.push(el);
        } else {
            wanted.push(el);
        }
    }

    // Touch only what differs: a node already in place is left exactly as is.
    let cursor = parent.firstElementChild;
    for (const el of wanted) {
        if (el === cursor) {
            cursor = cursor.nextElementSibling;
            continue;
        }
        parent.insertBefore(el, cursor);
    }
    while (cursor) {
        const stale = cursor;
        cursor = cursor.nextElementSibling;
        stale.remove();
    }
}

function render(): void {
    if (!snapshot) {
        const message = failure
            ? h('div', {class: 'blank', 'data-key': 'blank'},
                h('p', {class: 'blank-title'}, 'Không đọc được dữ liệu'),
                h('p', null, failure))
            : h('div', {class: 'blank', 'data-key': 'blank'},
                h('p', {class: 'blank-title'}, 'Đang đọc transcript của các session'),
                h('p', null, 'Lần đầu phải đọc toàn bộ nên mất vài giây. Các lần sau chỉ đọc phần mới.'));
        patch(content, h('div', null, message));
        return;
    }

    stamp.replaceChildren(
        failure ? 'Lỗi khi cập nhật, số liệu lúc ' : 'Cập nhật ',
        h('span', {class: 'stamp-time'}, f.clock(snapshot.generatedAtMs)),
    );
    stamp.classList.toggle('is-stale', failure !== '');
    stamp.title = failure;

    const extra = [optionsProblem, actionProblem].filter(Boolean);
    const shown: monitor.Snapshot = extra.length > 0
        ? {...snapshot, warnings: [...snapshot.warnings, ...extra]} as monitor.Snapshot
        : snapshot;
    view.open = chat.current();
    for (const t of Array.from(tabs.children) as HTMLElement[]) {
        const on = t.dataset.view === mode;
        t.classList.toggle('is-on', on);
        if (on) t.setAttribute('aria-current', 'page');
        else t.removeAttribute('aria-current');
    }
    document.body.dataset.mode = mode;

    if (mode === 'history') {
        pastState.fresh = Date.now() < riseUntil;
        // A day picked under another stretch may not be on the chart any more.
        if (pastState.day && !(history?.days ?? []).slice(-pastState.range).some(d => d.day === pastState.day)) pastState.day = '';
        const page = h('div', null,
            actionProblem && h('p', {class: 'warning hist-problem', 'data-key': 'problem'},
                h('span', {class: 'warning-mark', 'aria-hidden': 'true'}, '!'), actionProblem),
            past.historyView(history, pastState, historyProblem, view.budget));
        keepFocus(() => patch(content, page));
        drawTip();
        return;
    }
    hideTip();

    // A project the user added is worth showing even with no session in it.
    const nothing = shown.counts.sessions === 0 && shown.projects.length === 0;
    // With nothing to list there is nothing for the list switches to act on.
    switches.classList.toggle('is-idle', nothing);

    // The overview pairs who is at work (the caption and the floor) with what
    // it costs (the meter). Each part is patched on its own, so the floor's
    // figures keep moving while the figures in the meter change.
    const overview = h('div', {class: 'overview', 'data-key': 'overview', 'data-container': true},
        lead(shown), floor(shown, view.open.projectPath, view.hidden, view.showHidden), ...meter(shown, view, past.trend(history, TREND_DAYS)));
    const next = nothing
        ? h('div', null, nothingRunning(shown))
        : h('div', null, overview, view.ledgerOpen && ledger(shown), bays(shown, view));

    keepFocus(() => patch(content, next));

    if (pendingProject) {
        const bay = bayOf(pendingProject);
        pendingProject = '';
        bay?.scrollIntoView({behavior: 'auto', block: 'start'});
    }
}

// keepFocus runs a redraw and gives focus back to the control that had it, if
// the redraw replaced that control with its successor.
function keepFocus(draw: () => void): void {
    const focused = document.activeElement as HTMLElement | null;
    const focusKey = focused && content.contains(focused) ? focused.dataset.fkey : undefined;
    draw();
    if (focusKey && !content.contains(document.activeElement)) {
        // Without scrolling: the user may have scrolled elsewhere since.
        content.querySelector<HTMLElement>(`[data-fkey="${CSS.escape(focusKey)}"]`)?.focus({preventScroll: true});
    }
}

function bayOf(projectPath: string): HTMLElement | undefined {
    const key = `bay:${projectPath}`;
    return Array.from(content.querySelectorAll<HTMLElement>('.bay')).find(el => el.dataset.key === key);
}

function draw(): void {
    try {
        render();
    } catch (err) {
        // A rendering fault must not stop the refresh loop.
        failure = err instanceof Error ? err.message : String(err);
        console.error(err);
    }
}

async function refresh(): Promise<void> {
    if (loading) {
        stale = true;
        return;
    }
    loading = true;
    try {
        snapshot = await GetSnapshot();
        failure = '';
        chat.sync(snapshot);
    } catch (err) {
        failure = err instanceof Error ? err.message : String(err);
    } finally {
        loading = false;
    }
    draw();
    // The history follows at its own, slower pace.
    const wait = history?.scan.running ? HISTORY_SCAN_MS : mode === 'history' ? HISTORY_MS : HISTORY_IDLE_MS;
    if (Date.now() - historyAt >= wait) void loadHistory();
    if (stale) {
        stale = false;
        void refresh();
    }
}

async function loadHistory(): Promise<void> {
    if (historyLoading) return;
    historyLoading = true;
    historyAt = Date.now();
    try {
        history = await GetHistory(past.FETCH_DAYS);
        historyProblem = '';
    } catch (err) {
        historyProblem = err instanceof Error ? err.message : String(err);
    } finally {
        historyLoading = false;
    }
    draw();
}

function schedule(): void {
    window.clearTimeout(timer);
    if (document.hidden) return;
    timer = window.setTimeout(() => {
        void refresh().finally(schedule);
    }, FALLBACK_MS);
}

function setMode(to: Mode): void {
    if (mode === to) return;
    mode = to;
    actionProblem = '';
    if (to === 'history') {
        riseUntil = Date.now() + RISE_MS;
        void loadHistory();
    }
    window.scrollTo({top: 0});
    draw();
}

// ---- the readout of a day ------------------------------------------------------

function hideTip(): void {
    tipDay = '';
    tip.hidden = true;
}

// drawTip fills the readout for the day it is on and sets it beside that
// day's column, at the top of the plot and inside the window.
function drawTip(): void {
    if (!tipDay || !history || mode !== 'history') {
        hideTip();
        return;
    }
    const col = Array.from(content.querySelectorAll<HTMLElement>('.hc-col')).find(el => el.dataset.histDay === tipDay);
    const body = col ? past.dayReadout(history, pastState, tipDay) : null;
    if (!col || !body) {
        hideTip();
        return;
    }
    tip.replaceChildren(body);
    tip.hidden = false;
    const at = col.getBoundingClientRect();
    const frame = col.closest<HTMLElement>('.hc-frame')?.getBoundingClientRect() ?? at;
    const size = tip.getBoundingClientRect();
    // To the right of the day, or to its left when the window ends there:
    // the day itself and the figures around the chart stay in sight.
    const wide = document.documentElement.clientWidth;
    let left = at.right + 10;
    if (left + size.width > wide - 8) left = at.left - size.width - 10;
    left = Math.max(8, left);
    const top = Math.max(topBar.getBoundingClientRect().bottom + 4,
        Math.min(frame.top + 8, document.documentElement.clientHeight - size.height - 8));
    tip.style.left = `${Math.round(left)}px`;
    tip.style.top = `${Math.round(top)}px`;
}

function pointTip(target: EventTarget | null): void {
    const col = (target as HTMLElement | null)?.closest?.<HTMLElement>('.hc-col');
    const day = col?.dataset.histDay ?? '';
    if (day === tipDay) return;
    tipDay = day;
    drawTip();
}

content.addEventListener('pointerover', event => pointTip(event.target));
content.addEventListener('pointerleave', () => hideTip());
content.addEventListener('focusin', event => pointTip(event.target));
content.addEventListener('focusout', event => {
    if (!(event.relatedTarget as HTMLElement | null)?.closest?.('.hc-col')) hideTip();
});
window.addEventListener('scroll', () => {
    if (tipDay) drawTip();
}, {passive: true});

// The chart is one stop for the Tab key; the arrow keys walk its days.
content.addEventListener('keydown', event => {
    const col = (event.target as HTMLElement).closest<HTMLElement>('.hc-col');
    if (!col) return;
    const all = Array.from(col.parentElement?.querySelectorAll<HTMLElement>('.hc-col') ?? []);
    const i = all.indexOf(col);
    const to = event.key === 'ArrowLeft' ? i - 1 : event.key === 'ArrowRight' ? i + 1
        : event.key === 'Home' ? 0 : event.key === 'End' ? all.length - 1 : -1;
    if (to < 0 || to >= all.length || to === i) return;
    event.preventDefault();
    all[to].focus();
});

content.addEventListener('change', event => {
    const box = event.target as HTMLInputElement;
    if (!box.matches?.('[data-hist-budget]')) return;
    const n = box.value.trim() === '' ? 0 : Number(box.value);
    if (!Number.isFinite(n) || n < 0 || n > 1_000_000) {
        actionProblem = 'Ngưỡng chi phí phải là một số từ 0 tới 1.000.000 USD.';
        render();
        return;
    }
    view.budget = n;
    rememberText('budget', String(n));
    void SetBudget(n).then(problem => {
        actionProblem = problem;
        render();
    }, err => {
        actionProblem = err instanceof Error ? err.message : String(err);
        render();
    });
});

// ---- interaction --------------------------------------------------------------

content.addEventListener('click', event => {
    const target = event.target as HTMLElement;

    const go = target.closest<HTMLElement>('[data-view]');
    if (go) {
        setMode(go.dataset.view as Mode);
        return;
    }

    // ---- the history page ----
    const range = target.closest<HTMLElement>('[data-hist-range]');
    if (range) {
        pastState.range = Number(range.dataset.histRange) as past.Range;
        rememberText('histRange', String(pastState.range));
        riseUntil = Date.now() + RISE_MS;
        render();
        return;
    }
    const split = target.closest<HTMLElement>('[data-hist-split]');
    if (split) {
        pastState.split = split.dataset.histSplit as past.Split;
        rememberText('histSplit', pastState.split);
        riseUntil = Date.now() + RISE_MS;
        render();
        return;
    }
    const day = target.closest<HTMLElement>('[data-hist-day]');
    if (day) {
        // A day that is picked again is let go.
        pastState.day = pastState.day === day.dataset.histDay ? '' : day.dataset.histDay!;
        render();
        return;
    }
    if (target.closest('[data-hist-table]')) {
        pastState.tableOpen = !pastState.tableOpen;
        render();
        return;
    }
    if (target.closest('[data-hist-export]')) {
        void ExportHistory(pastState.range).then(problem => {
            actionProblem = problem;
            render();
        }, err => {
            actionProblem = err instanceof Error ? err.message : String(err);
            render();
        });
        return;
    }

    const fold = target.closest<HTMLElement>('[data-toggle-session]');
    if (fold) {
        const id = fold.dataset.toggleSession!;
        if (view.expanded.has(id)) view.expanded.delete(id);
        else view.expanded.add(id);
        render();
        return;
    }

    if (target.closest('[data-toggle-ledger]')) {
        view.ledgerOpen = !view.ledgerOpen;
        remember('ledgerOpen', view.ledgerOpen);
        render();
        return;
    }

    // Taking a project off the board, and putting it back.
    const hide = target.closest<HTMLElement>('[data-hide-project]');
    if (hide) {
        view.hidden.add(hide.dataset.hideProject!);
        rememberHidden();
        render();
        return;
    }
    const show = target.closest<HTMLElement>('[data-show-project]');
    if (show) {
        view.hidden.delete(show.dataset.showProject!);
        if (view.hidden.size === 0) view.showHidden = false;
        rememberHidden();
        render();
        return;
    }
    if (target.closest('[data-toggle-hidden]')) {
        view.showHidden = !view.showHidden;
        render();
        return;
    }

    const desk = target.closest<HTMLElement>('[data-scroll-project]');
    if (desk) {
        // A desk on the floor leads to its project's block further down.
        const bay = bayOf(desk.dataset.scrollProject!);
        const calm = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
        bay?.scrollIntoView({behavior: calm ? 'auto' : 'smooth', block: 'start'});
        return;
    }

    // Actions that ask the Go side to do something and may come back with a
    // reason it could not: the reason is shown with the page's other notices.
    const failed = (err: unknown): void => {
        actionProblem = err instanceof Error ? err.message : String(err);
        render();
    };
    const report = (call: Promise<string>): void => {
        void call.then(problem => {
            actionProblem = problem;
            void refresh();
        }, failed);
    };

    if (target.closest('[data-add-project]')) {
        // A folder that is added is wanted on the board: if it was hidden
        // before, it comes back.
        const added = new Set((snapshot?.projects ?? []).filter(p => (p as monitor.Project & { pinned?: boolean }).pinned).map(p => p.path));
        void AddProject().then(async problem => {
            actionProblem = problem;
            await refresh();
            let back = false;
            for (const p of snapshot?.projects ?? []) {
                if ((p as monitor.Project & { pinned?: boolean }).pinned && !added.has(p.path) && view.hidden.delete(p.path)) back = true;
            }
            if (back) {
                rememberHidden();
                render();
            }
        }, failed);
        return;
    }

    const remove = target.closest<HTMLElement>('[data-remove-project]');
    if (remove) {
        report(RemoveProject(remove.dataset.removeProject!));
        return;
    }

    const fresh = target.closest<HTMLElement>('[data-new-session]');
    if (fresh) {
        const path = fresh.dataset.newSession!;
        const name = fresh.dataset.projectName ?? '';
        // A session may only be started in a folder the user has agreed to.
        // For one that is on the board only because something runs there,
        // the app asks now, in a dialog of its own.
        void TrustProject(path).then(res => {
            actionProblem = res.problem;
            if (res.ok) chat.openDraft(path, name);
            void refresh();
        }, failed);
        return;
    }

    const editor = target.closest<HTMLElement>('[data-open-project]');
    if (editor) {
        report(OpenProject(editor.dataset.openProject!));
        return;
    }

    // What is left is a click on a row. One made while selecting text is not
    // a request to open anything.
    const onButton = target.closest('button') !== null;
    if (!onButton && (window.getSelection()?.toString() ?? '') !== '') return;

    const talk = target.closest<HTMLElement>('[data-open-chat]');
    if (talk) {
        chat.open(talk.dataset.openChat!);
        return;
    }

    const row = target.closest<HTMLElement>('[data-open-trail]');
    if (row) {
        const [sessionId, agentId = ''] = row.dataset.openTrail!.split('|');
        chat.openTrail(sessionId, agentId);
    }
});

credit.addEventListener('click', event => {
    const link = (event.target as HTMLElement).closest<HTMLElement>('[data-credit]');
    if (!link) return;
    const show = (problem: string): void => {
        actionProblem = problem;
        render();
    };
    void OpenCredit(link.dataset.credit!).then(show, err => show(err instanceof Error ? err.message : String(err)));
});

// The side panel sits beside the board, outside the part that is redrawn. The
// board marks what the panel shows, so it is drawn again when that changes.
chat.mount(root, () => render());

// goTo takes the page to what a notification was about: the conversation of
// a session of the app's own, or else the project's place on the board.
function goTo(to: { projectPath?: string; sessionId?: string; chat?: boolean }): void {
    setMode('board');
    if (to.chat && to.sessionId) {
        chat.open(to.sessionId);
        return;
    }
    pendingProject = to.projectPath ?? '';
    void refresh();
}
notices.mount(root, goTo);

tabs.addEventListener('click', event => {
    const to = (event.target as HTMLElement).closest<HTMLElement>('[data-view]');
    if (to) setMode(to.dataset.view as Mode);
});
document.addEventListener('visibilitychange', () => {
    if (document.hidden) {
        window.clearTimeout(timer);
        return;
    }
    void refresh().finally(schedule);
});

// "/" goes to the filter, from anywhere a key is not being typed into a box.
document.addEventListener('keydown', event => {
    if (event.key !== '/' || event.ctrlKey || event.altKey || event.metaKey) return;
    const el = event.target as HTMLElement;
    if (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName)) return;
    event.preventDefault();
    setMode('board');
    find.focus();
    find.select();
});

// The Go side scans on its own clock and says when a scan is ready; the page
// fetches it then, unless nobody is looking.
EventsOn('snapshot', () => {
    if (document.hidden) return;
    void refresh().finally(schedule);
});
EventsOn('history', () => void loadHistory());
// A notification of Windows was clicked: go to what it was about.
EventsOn('focus', (to: { projectPath?: string; sessionId?: string; chat?: boolean } | null) => {
    if (to) goTo(to);
});
// Everything the app says is also kept in the window; see notices.ts.
EventsOn('notice', n => notices.add(n));

render();
void pushOptions(false);
if (view.budget > 0) void SetBudget(view.budget).catch(() => { /* said again when it is next changed */ });
void refresh().finally(schedule);
void loadHistory();
