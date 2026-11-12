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

import {AddProject, GetSnapshot, OpenCredit, OpenProject, RemoveProject, SetOptions, TrustProject} from '../wailsjs/go/main/App';
import {monitor} from '../wailsjs/go/models';
import * as chat from './chat';
import * as f from './format';
import {bays, h, lead, ledger, meter, nothingRunning, ViewState} from './render';
import {floor} from './scene';

const REFRESH_MS = 2000;

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

const view: ViewState = {
    hideEnded: recall('hideEnded'),
    privacy: recall('privacy'),
    expanded: new Set(),
    ledgerOpen: recall('ledgerOpen'),
    open: {sessionId: '', agentId: '', projectPath: ''},
};
let notify = recall('notify');
let snapshot: monitor.Snapshot | null = null;
let failure = '';
let optionsProblem = '';
// What the last action on a project could not do. It is kept apart from the
// switches' problem: one must not wipe out the other.
let actionProblem = '';
let timer: number | undefined;
let loading = false;

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
        'Hiện thông báo của Windows khi một session xong lượt hoặc một agent làm xong',
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
        'Giấu câu lệnh, yêu cầu và kết quả, dùng khi chia sẻ màn hình. Tên project, số liệu và tên tool vẫn hiện.',
        view.privacy, on => {
            view.privacy = on;
            remember('privacy', on);
            void pushOptions(false);
            render();
        }),
);

const topBar = h('header', {class: 'top'},
    h('span', {class: 'brand'}, 'Agent Desk'),
    h('div', {class: 'top-right'}, switches, stamp),
);
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
root.append(topBar, content, credit);

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
    // A project the user added is worth showing even with no session in it.
    const nothing = shown.counts.sessions === 0 && shown.projects.length === 0;
    // With nothing to list there is nothing for the list switches to act on.
    switches.classList.toggle('is-idle', nothing);

    // The overview pairs who is at work (the caption and the floor) with what
    // it costs (the meter). Each part is patched on its own, so the floor's
    // figures keep moving while the figures in the meter change.
    const overview = h('div', {class: 'overview', 'data-key': 'overview', 'data-container': true},
        lead(shown), floor(shown, view.open.projectPath), ...meter(shown, view));
    const next = nothing
        ? h('div', null, nothingRunning(shown))
        : h('div', null, overview, view.ledgerOpen && ledger(shown), bays(shown, view));

    const focused = document.activeElement as HTMLElement | null;
    const focusKey = focused && content.contains(focused) ? focused.dataset.fkey : undefined;
    patch(content, next);
    if (focusKey && !content.contains(document.activeElement)) {
        // The focused control was redrawn. Give focus back to its successor
        // without scrolling: the user may have scrolled elsewhere since.
        content.querySelector<HTMLElement>(`[data-fkey="${CSS.escape(focusKey)}"]`)?.focus({preventScroll: true});
    }
}

async function refresh(): Promise<void> {
    if (loading) return;
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
    try {
        render();
    } catch (err) {
        // A rendering fault must not stop the refresh loop.
        failure = err instanceof Error ? err.message : String(err);
        console.error(err);
    }
}

function schedule(): void {
    window.clearTimeout(timer);
    if (document.hidden) return;
    timer = window.setTimeout(() => {
        void refresh().finally(schedule);
    }, REFRESH_MS);
}

// ---- interaction --------------------------------------------------------------

content.addEventListener('click', event => {
    const target = event.target as HTMLElement;

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

    const desk = target.closest<HTMLElement>('[data-scroll-project]');
    if (desk) {
        // A desk on the floor leads to its project's block further down.
        const key = `bay:${desk.dataset.scrollProject}`;
        const bay = Array.from(content.querySelectorAll<HTMLElement>('.bay')).find(el => el.dataset.key === key);
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
        report(AddProject());
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
document.addEventListener('visibilitychange', () => {
    if (document.hidden) {
        window.clearTimeout(timer);
        return;
    }
    void refresh().finally(schedule);
});

render();
void pushOptions(false);
void refresh().finally(schedule);
