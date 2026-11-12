import {GetNotices} from '../wailsjs/go/main/App';
import {main} from '../wailsjs/go/models';
import * as f from './format';
import {h} from './render';

// What the app has told the user, kept in the window itself.
//
// A Windows notification is the first way the app says that something is
// done or needs an answer, but Windows may be set to show none, and then it
// drops them without a word. So everything said is also listed here: a card
// that stays a few seconds while the window is in view, and a list behind the
// button in the top bar, with a count of what has not been looked at yet.

type Notice = main.Notice;

// How long a card stays, and how many are on screen at once.
const CARD_MS = 9000;
const CARDS = 3;

let list: Notice[] = [];
// The id of the newest notice the user has had in front of them.
let seen = 0;
let open = false;
let go: (n: Notice) => void = () => {};

const count = h('span', {class: 'bell-count', hidden: true});
/** The button for the top bar. */
export const bell = h('button', {
    type: 'button', class: 'bell', 'aria-haspopup': 'true', 'aria-expanded': 'false',
    title: 'Những gì app vừa báo: session xong lượt, session cần bạn trả lời, agent làm xong',
}, 'Thông báo', count);
const panel = h('div', {class: 'bell-panel', role: 'region', 'aria-label': 'Thông báo gần đây', hidden: true});
const cards = h('div', {class: 'toasts', role: 'status', 'aria-live': 'polite'});

function unread(): number {
    return list.filter(n => n.id > seen).length;
}

function drawCount(): void {
    const n = unread();
    count.hidden = n === 0;
    count.textContent = n > 99 ? '99+' : String(n);
    bell.setAttribute('aria-label', n > 0 ? `Thông báo, ${n} cái chưa xem` : 'Thông báo');
}

function words(n: Notice): HTMLElement[] {
    return [
        h('span', {class: 'note-title'}, n.title),
        ...(n.body ? [h('span', {class: 'note-body'}, n.body)] : []),
    ];
}

function drawPanel(): void {
    if (!open) return;
    if (list.length === 0) {
        panel.replaceChildren(h('p', {class: 'bell-none'},
            'Chưa có gì để báo. App báo ở đây khi một session xong lượt, khi nó dừng lại chờ bạn trả lời, và khi một agent làm xong.'));
        return;
    }
    const rows = [...list].reverse().map(n => h('button', {type: 'button', class: 'bell-item', 'data-notice': n.id},
        h('span', {class: 'note-time'}, f.hourMinute(n.atMs)),
        h('span', {class: 'note-words'}, ...words(n))));
    panel.replaceChildren(...rows);
}

// place sets the list under its button, its right edge on the button's.
function place(): void {
    const at = bell.getBoundingClientRect();
    panel.style.top = `${Math.round(at.bottom + 6)}px`;
    panel.style.right = `${Math.max(8, Math.round(document.documentElement.clientWidth - at.right))}px`;
}

function setOpen(to: boolean): void {
    open = to;
    panel.hidden = !to;
    bell.setAttribute('aria-expanded', String(to));
    if (!to) return;
    // Opening the list is looking at it.
    seen = list.reduce((m, n) => Math.max(m, n.id), seen);
    cards.replaceChildren();
    drawCount();
    drawPanel();
    place();
}

function card(n: Notice): void {
    const el = h('button', {type: 'button', class: 'toast', 'data-notice': n.id}, ...words(n));
    cards.append(el);
    while (cards.childElementCount > CARDS) cards.firstElementChild?.remove();
    window.setTimeout(() => el.remove(), CARD_MS);
}

function byId(target: EventTarget | null): Notice | undefined {
    const id = Number((target as HTMLElement | null)?.closest<HTMLElement>('[data-notice]')?.dataset.notice);
    return list.find(n => n.id === id);
}

/** Takes in a notice the Go side has just sent. */
export function add(n: Notice): void {
    if (!n || list.some(x => x.id === n.id)) return;
    list.push(n);
    if (list.length > 60) list = list.slice(-60);
    if (open) {
        seen = Math.max(seen, n.id);
        drawPanel();
    } else if (!document.hidden) {
        card(n);
    }
    drawCount();
}

/** Mounts the list and its cards. onGo is called with a notice that was clicked. */
export function mount(root: HTMLElement, onGo: (n: Notice) => void): void {
    go = onGo;
    root.append(panel, cards);

    bell.addEventListener('click', () => setOpen(!open));
    const pick = (event: Event) => {
        const n = byId(event.target);
        if (!n) return;
        seen = Math.max(seen, n.id);
        setOpen(false);
        (event.target as HTMLElement).closest('.toast')?.remove();
        drawCount();
        go(n);
    };
    panel.addEventListener('click', pick);
    cards.addEventListener('click', pick);
    document.addEventListener('click', event => {
        const el = event.target as Node;
        if (open && !panel.contains(el) && !bell.contains(el)) setOpen(false);
    });
    document.addEventListener('keydown', event => {
        if (event.key === 'Escape' && open) {
            setOpen(false);
            bell.focus();
        }
    });
    window.addEventListener('resize', () => {
        if (open) place();
    });

    // What was said before the page loaded (after a reload, say) is in the
    // list, and counts as seen: it is not news any more.
    GetNotices().then(got => {
        const known = new Set(list.map(n => n.id));
        const before = (got ?? []).filter(n => !known.has(n.id));
        seen = before.reduce((m, n) => Math.max(m, n.id), seen);
        list = [...before, ...list].sort((a, b) => a.id - b.id);
        drawCount();
        drawPanel();
    }, () => { /* the list starts empty */ });
}
