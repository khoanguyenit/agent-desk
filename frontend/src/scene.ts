import {monitor} from '../wailsjs/go/models';
import * as f from './format';
import {h} from './render';

// The floor: a small office drawn from the snapshot. Each project is a desk;
// at it sits one figure for every session and for every agent that is still
// working. A figure types only while its session or agent is running, so the
// motion on screen is exactly the work in progress.
//
// The figures are plain elements styled and animated in the stylesheet. Their
// markup changes only when who-is-working changes, so the page's refresh keeps
// the same nodes and the animation runs on without restarting.

type WorkerState = 'running' | 'waiting' | 'quiet' | 'idle';

interface Worker {
    lead: boolean;
    state: WorkerState;
}

// How many figures a desk seats before the rest become a "+N".
const SEATS = 8;

const sessionIsLive = (s: monitor.Session) => s.status === 'busy' || s.status === 'waiting' || s.activeAgents > 0;

function workersOf(p: monitor.Project): { workers: Worker[]; ended: number } {
    const leads: Worker[] = [];
    const agents: Worker[] = [];
    let ended = 0;
    for (const s of p.sessions) {
        const own = s as monitor.Session & { duplicate?: boolean; awaitingUser?: boolean };
        if (own.duplicate) continue;
        // A session that has stopped to ask for permission is held up, and
        // is drawn so even though its process still counts as busy.
        leads.push({lead: true, state: own.awaitingUser || s.status === 'waiting' ? 'waiting' : s.status === 'busy' ? 'running' : 'idle'});
        for (const a of s.agents) {
            if (a.status === 'running') {
                agents.push({lead: false, state: 'running'});
            } else if (a.status === 'quiet') {
                const waiting = (a as monitor.Agent & { awaitingTool?: boolean }).awaitingTool;
                agents.push({lead: false, state: waiting ? 'waiting' : 'quiet'});
            } else {
                ended++;
            }
        }
    }
    // Those at work sit together, next to the session's own figure.
    const order: WorkerState[] = ['running', 'waiting', 'quiet', 'idle'];
    agents.sort((a, b) => order.indexOf(a.state) - order.indexOf(b.state));
    return {workers: [...leads, ...agents], ended};
}

function figure(w: Worker, index: number): HTMLElement {
    // The bubble over the head says what the figure is doing: three dots
    // while typing, a pause mark while waiting or quiet, nothing when idle.
    let bubble: HTMLElement | null = null;
    if (w.state === 'running') {
        bubble = h('span', {class: 'w-bubble w-typing'}, h('i'), h('i'), h('i'));
    } else if (w.state === 'waiting' || w.state === 'quiet') {
        bubble = h('span', {class: 'w-bubble w-paused'}, h('i'), h('i'));
    }
    // A stagger class keeps neighbours from moving in step.
    return h('span', {class: `worker is-${w.state}${w.lead ? ' is-lead' : ''} d${index % 5}`},
        bubble,
        h('span', {class: 'w-head'}),
        h('span', {class: 'w-body'}),
        h('span', {class: 'w-laptop'}, h('span', {class: 'w-lamp'})),
    );
}

function count(n: number, what: string): string {
    return n > 0 ? `${n} ${what}` : '';
}

function desk(p: monitor.Project, open: boolean): HTMLElement {
    const {workers, ended} = workersOf(p);
    const running = workers.filter(w => w.state === 'running').length;
    const held = workers.filter(w => w.state === 'waiting' || w.state === 'quiet').length;

    const seats = h('span', {class: 'desk-seats', 'data-key': 'seats', 'aria-hidden': 'true'});
    workers.slice(0, SEATS).forEach((w, i) => seats.append(figure(w, i)));
    if (workers.length > SEATS) seats.append(h('span', {class: 'desk-more'}, `+${workers.length - SEATS}`));

    // On screen the desk says what is happening now; the finished count is
    // history, so only a screen reader hears it.
    // A project whose sessions have all closed keeps its desk for the day,
    // empty: what it cost is still part of the total.
    const closed = ((p as monitor.Project & { closed?: unknown[] }).closed ?? []).length;
    const now = [count(running, 'đang chạy'), count(held, 'đang chờ')].filter(Boolean).join(' · ')
        || (p.sessions.length > 0 ? 'đang rảnh' : closed > 0 ? `${closed} session đã đóng` : 'chưa có session');

    // Who sits where changes only when work starts or stops, and the key
    // carries it: a desk is redrawn whole then, and otherwise only its cost,
    // which moves on every refresh, is replaced, so the figures keep moving
    // without a restart.
    const shape = workers.map(w => (w.lead ? w.state.toUpperCase() : w.state)[0]).join('') + `-${ended}`;
    // The desk whose session is open in the side panel is the one the user
    // is sitting at.
    const state = (running > 0 ? ' is-live' : held > 0 ? ' is-held' : '') + (open ? ' is-open' : '');

    return h('button', {
        type: 'button',
        class: 'desk' + state,
        'data-key': `desk:${p.path}:${shape}:${open ? 'open' : ''}`,
        'data-container': true,
        'data-scroll-project': p.path,
        'data-fkey': `desk-${p.path}`,
        title: `Tới phần của ${p.name} bên dưới`,
    },
        seats,
        h('span', {class: 'desk-name', 'data-key': 'name'}, p.name),
        h('span', {class: 'desk-meta', 'data-key': 'meta'},
            now,
            ended > 0 && h('span', {class: 'vh'}, `, ${ended} đã xong`),
        ),
        h('span', {
            class: 'desk-cost', 'data-key': 'cost',
            title: `Hôm nay ${f.usd(p.today.costUsd)} · tổng ${f.usd(p.all.costUsd)} · ${f.tokens(p.all.totalTokens)} token`,
        }, h('span', {class: 'vh'}, 'chi phí '), f.usd(p.all.costUsd)),
    );
}

export function floor(snap: monitor.Snapshot, openProject: string): HTMLElement {
    const live = snap.projects.filter(p => p.sessions.some(sessionIsLive));
    const rest = snap.projects.filter(p => !p.sessions.some(sessionIsLive));
    const el = h('section', {class: 'floor', 'data-key': 'floor', 'data-container': true, 'aria-label': 'Các project đang mở'});
    for (const p of [...live, ...rest]) el.append(desk(p, p.path === openProject));
    // An empty place at the end of the floor: where the next project goes.
    el.append(h('button', {
        type: 'button', class: 'desk desk-add', 'data-key': 'desk:add', 'data-add-project': true, 'data-fkey': 'add-project',
        title: 'Thêm một thư mục project để theo dõi và mở session ngay trong app',
    },
        h('span', {class: 'desk-seats', 'aria-hidden': 'true'}, h('span', {class: 'desk-plus'}, '+')),
        h('span', {class: 'desk-name'}, 'Thêm project'),
        h('span', {class: 'desk-meta'}, 'chọn một thư mục'),
    ));
    return el;
}
