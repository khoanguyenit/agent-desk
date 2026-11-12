import {monitor} from '../wailsjs/go/models';
import * as f from './format';

// Everything on the page is built with DOM nodes and text content. Transcript
// text (prompts, commands, file names) is never parsed as HTML.

type Attrs = Record<string, string | number | boolean | null | undefined>;
type Child = Node | string | number | null | undefined | false;

export function h<K extends keyof HTMLElementTagNameMap>(
    tag: K,
    attrs?: Attrs | null,
    ...children: Child[]
): HTMLElementTagNameMap[K] {
    const el = document.createElement(tag);
    if (attrs) {
        for (const [name, value] of Object.entries(attrs)) {
            if (value === null || value === undefined || value === false) continue;
            el.setAttribute(name, value === true ? '' : String(value));
        }
    }
    for (const child of children) {
        if (child === null || child === undefined || child === false) continue;
        el.append(child instanceof Node ? child : String(child));
    }
    return el;
}

const SVG_NS = 'http://www.w3.org/2000/svg';
export type IconName = 'dot' | 'ring' | 'pause' | 'square' | 'check';

export function icon(name: IconName): SVGElement {
    const svg = document.createElementNS(SVG_NS, 'svg');
    svg.setAttribute('viewBox', '0 0 12 12');
    svg.setAttribute('class', `ic ic-${name}`);
    svg.setAttribute('aria-hidden', 'true');
    const shape = (tag: string, attrs: Record<string, string>) => {
        const el = document.createElementNS(SVG_NS, tag);
        for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
        svg.append(el);
    };
    switch (name) {
        case 'dot':
            shape('circle', {cx: '6', cy: '6', r: '4', fill: 'currentColor'});
            break;
        case 'ring':
            shape('circle', {cx: '6', cy: '6', r: '3.6', fill: 'none', stroke: 'currentColor', 'stroke-width': '1.8'});
            break;
        case 'pause':
            shape('rect', {x: '2.6', y: '2', width: '2.6', height: '8', rx: '0.8', fill: 'currentColor'});
            shape('rect', {x: '6.8', y: '2', width: '2.6', height: '8', rx: '0.8', fill: 'currentColor'});
            break;
        case 'square':
            shape('rect', {x: '2.5', y: '2.5', width: '7', height: '7', rx: '1.2', fill: 'currentColor'});
            break;
        case 'check':
            shape('path', {
                d: 'M2.4 6.5 5 9 9.7 3.5', fill: 'none', stroke: 'currentColor',
                'stroke-width': '1.8', 'stroke-linecap': 'round', 'stroke-linejoin': 'round',
            });
            break;
    }
    return svg;
}

export interface ViewState {
    // hideEnded leaves only the agents that are still working.
    hideEnded: boolean;
    // privacy keeps prompt, command and report text off the screen.
    privacy: boolean;
    expanded: Set<string>;
    // ledgerOpen shows the full cost tables under the overview.
    ledgerOpen: boolean;
    // open is what the side panel is showing, so the board can mark it.
    open: OpenMark;
    // filter is what was typed in the search box: only rows that carry it
    // are listed.
    filter: string;
    // budget is the cost per day, in dollars, the user wants to stay under;
    // zero when none is set.
    budget: number;
}

export interface OpenMark {
    sessionId: string;
    agentId: string;
    projectPath: string;
}

// Fields newer builds of the backend add. They are optional here so the page
// keeps working against a snapshot that lacks them.
interface CostParts {
    input: number;
    output: number;
    cacheRead: number;
    cacheWrite: number;
    webSearch: number;
}
type Spend = monitor.Spend & { costParts?: CostParts };
type Agent = monitor.Agent & { awaitingTool?: boolean };
type Session = monitor.Session & { duplicate?: boolean; managed?: boolean; awaitingUser?: boolean };
type Project = monitor.Project & { pinned?: boolean; closed?: monitor.ClosedSession[] };

// fold makes text comparable the way a person types it into a search box:
// without capitals and without the marks of Vietnamese.
function fold(s: string): string {
    return s.normalize('NFD').replace(/[\u0300-\u036f]/g, '').replace(/đ/g, 'd').replace(/Đ/g, 'D').toLowerCase();
}

// carries returns the test a row must pass to be listed under the filter.
function carries(filter: string): (...texts: (string | undefined | null)[]) => boolean {
    const q = fold(filter.trim());
    return (...texts) => q === '' || texts.some(t => Boolean(t) && fold(t!).includes(q));
}

// How many ended agents a session lists before the rest are folded away.
const ENDED_SHOWN = 6;

const isActive = (a: monitor.Agent) => a.status === 'running' || a.status === 'quiet';
const sessionIsLive = (s: monitor.Session) => s.status === 'busy' || s.activeAgents > 0;

function warningLine(text: string): HTMLElement {
    return h('p', {class: 'warning'}, h('span', {class: 'warning-mark', 'aria-hidden': 'true'}, '!'), text);
}

// ---- top of the page ---------------------------------------------------------

export function lead(snap: monitor.Snapshot): HTMLElement {
    const c = snap.counts;
    // A session that has stopped to ask for permission still counts as busy
    // to Claude Code. Here it is counted as what it is: waiting for the user.
    const asking = snap.projects.reduce((n, p) => n + p.sessions.filter(s => (s as Session).awaitingUser && !(s as Session).duplicate).length, 0);
    const busy = Math.max(0, c.busySessions - asking);
    const live = busy > 0 || c.runningAgents > 0;
    let headline: string;
    if (busy > 0 && c.runningAgents > 0) {
        headline = `${busy} session và ${c.runningAgents} agent đang chạy`;
    } else if (busy > 0) {
        headline = `${busy} session đang chạy`;
    } else if (c.runningAgents > 0) {
        headline = `${c.runningAgents} agent đang chạy`;
    } else if (asking > 0) {
        headline = '';
    } else {
        headline = 'Mọi session đang rảnh';
    }

    // The sentence is the floor's caption: the figures below show who, this
    // line says how many. The money sits beside it, in the meter.
    return h('section', {class: 'lead', 'data-key': 'lead'},
        h('h1', {class: 'headline'},
            live && h('span', {class: 'lamp', 'aria-hidden': 'true'}),
            headline && h('span', null, headline),
            asking > 0 && h('span', {class: (headline ? 'headline-quiet ' : '') + 'headline-asking'}, icon('pause'), `${asking} session chờ bạn cho phép`),
            c.quietAgents > 0 && h('span', {class: 'headline-quiet'}, icon('pause'), `${c.quietAgents} agent im lặng`),
        ),
    );
}

function costCell(sp: monitor.Spend): HTMLElement {
    const td = h('td', {class: 'num'}, f.usd(sp.costUsd));
    if (sp.unpricedTokens > 0) {
        td.append(h('span', {class: 'unpriced', title: `Chưa gồm ${f.tokensExact(sp.unpricedTokens)} token của model chưa có giá`}, '+?'));
    }
    return td;
}

function tokenCell(sp: monitor.Spend): HTMLElement {
    return h('td', {class: 'num', title: `${f.tokensExact(sp.totalTokens)} token`}, f.tokens(sp.totalTokens));
}

function groupHeader(label: string, title?: string): HTMLElement {
    return h('th', {scope: 'colgroup', colspan: 2, class: 'group', title}, h('span', null, label));
}

const sinceStart = 'Cộng từ lúc bắt đầu của mỗi session trên trang: các session đang mở và các session đã đóng trong hôm nay';

// The sessions of a project that ended but were at work today.
const closedOf = (p: monitor.Project): monitor.ClosedSession[] => (p as Project).closed ?? [];

function spendTable(snap: monitor.Snapshot): HTMLElement {
    const head = h('thead', null,
        h('tr', null,
            h('th', {scope: 'col', rowspan: 2, class: 'txt'}, 'Project'),
            h('th', {scope: 'col', rowspan: 2, class: 'num', title: 'Số agent chưa xong (đang chạy hoặc im lặng) trên tổng số agent đã gọi'}, 'Chưa xong / tổng'),
            groupHeader('Hôm nay'),
            groupHeader('Tổng', sinceStart),
        ),
        h('tr', null,
            h('th', {scope: 'col', class: 'num sub'}, 'Token'),
            h('th', {scope: 'col', class: 'num sub'}, 'Chi phí'),
            h('th', {scope: 'col', class: 'num sub'}, 'Token'),
            h('th', {scope: 'col', class: 'num sub'}, 'Chi phí'),
        ),
    );

    const body = h('tbody');
    let activeAll = 0;
    let agentsAll = 0;
    for (const p of snap.projects) {
        let active = 0;
        let agents = 0;
        for (const s of p.sessions) {
            active += s.activeAgents;
            agents += s.agents.length;
        }
        activeAll += active;
        agentsAll += agents;
        body.append(h('tr', null,
            h('th', {scope: 'row', class: 'txt', title: p.path},
                h('span', {class: 'row-name'}, p.name),
                h('span', {class: 'row-meta'}, [
                    p.sessions.length > 1 ? `${p.sessions.length} session` : '',
                    closedOf(p).length > 0 ? `${closedOf(p).length} đã đóng` : '',
                ].filter(Boolean).join(' · ')),
            ),
            h('td', {class: 'num' + (active > 0 ? ' has-active' : '')}, `${active} / ${agents}`),
            tokenCell(p.today),
            costCell(p.today),
            tokenCell(p.all),
            costCell(p.all),
        ));
    }

    const foot = h('tfoot', null,
        h('tr', null,
            h('th', {scope: 'row', class: 'txt'}, 'Tất cả'),
            h('td', {class: 'num'}, `${activeAll} / ${agentsAll}`),
            tokenCell(snap.todaySpend),
            costCell(snap.todaySpend),
            tokenCell(snap.all),
            costCell(snap.all),
        ),
    );

    return h('table', {class: 'grid spend-table'}, head, body, foot);
}

interface Part {
    key: string;
    name: string;
    tokens: number;
    cost: number;
}

function tokenParts(sp: Spend): { parts: Part[]; hasCost: boolean } {
    const tk = sp.tokens;
    const c = sp.costParts;
    return {
        hasCost: c !== undefined,
        parts: [
            {key: 's1', name: 'Cache đọc', tokens: tk.cacheRead, cost: c?.cacheRead ?? 0},
            {key: 's2', name: 'Cache ghi', tokens: tk.cacheWrite5m + tk.cacheWrite1h, cost: c?.cacheWrite ?? 0},
            {key: 's3', name: 'Output', tokens: tk.output, cost: c?.output ?? 0},
            {key: 's4', name: 'Input', tokens: tk.input, cost: c?.input ?? 0},
        ],
    };
}

// The bar shows where the money goes, which is not where the tokens go: cache
// reads are most of the tokens and a much smaller share of the cost. Without
// cost per category it falls back to token share.
interface Mix {
    parts: Part[];
    hasCost: boolean;
    measure: (p: Part) => number;
    whole: number;
    what: string;
}

function mixOf(sp: Spend): Mix {
    const {parts, hasCost} = tokenParts(sp);
    const measure = (p: Part) => (hasCost ? p.cost : p.tokens);
    return {
        parts, hasCost, measure,
        whole: parts.reduce((sum, p) => sum + measure(p), 0),
        what: hasCost ? 'chi phí' : 'token',
    };
}

function segbar(mix: Mix): HTMLElement {
    const {parts, measure, whole, what} = mix;
    const described = parts.map(p => `${p.name} ${f.percent(measure(p), whole)}`).join(', ');
    const bar = h('div', {class: 'segbar', role: 'img', 'aria-label': `Tỷ trọng ${what} theo loại token: ${described}`});
    for (const p of parts) {
        // A part too small to draw is left to the figures beside it.
        if (whole <= 0 || measure(p) / whole < 0.002) continue;
        const block = h('span', {class: `seg seg-${p.key}`, title: `${p.name}: ${f.percent(measure(p), whole)} ${what}`});
        // Shares as whole numbers: growth factors that sum to less than one
        // would leave the bar only partly filled.
        block.style.flexGrow = String(Math.max(1, Math.round((measure(p) / whole) * 10_000)));
        bar.append(block);
    }
    return bar;
}

function tokenMix(snap: monitor.Snapshot): HTMLElement {
    const mix = mixOf(snap.all as Spend);
    const {parts, hasCost, measure, whole, what} = mix;
    const bar = segbar(mix);

    const rows = h('tbody');
    for (const p of parts) {
        rows.append(h('tr', null,
            h('th', {scope: 'row', class: 'txt'},
                h('span', {class: 'key'}, h('span', {class: `swatch seg-${p.key}`, 'aria-hidden': 'true'}), p.name)),
            h('td', {class: 'num', title: `${f.tokensExact(p.tokens)} token`}, f.tokens(p.tokens)),
            hasCost && h('td', {class: 'num'}, f.usd(p.cost)),
            h('td', {class: 'num share'}, f.percent(measure(p), whole)),
        ));
    }

    return h('div', {class: 'block'},
        h('h2', {class: 'block-title'}, 'Theo loại token'),
        bar,
        h('table', {class: 'grid mix-table'},
            h('thead', null, h('tr', null,
                h('th', {scope: 'col', class: 'txt'}, 'Loại'),
                h('th', {scope: 'col', class: 'num'}, 'Token'),
                hasCost && h('th', {scope: 'col', class: 'num'}, 'Chi phí'),
                h('th', {scope: 'col', class: 'num', title: `Phần của mỗi loại trong tổng ${what}; thanh phía trên vẽ theo cột này`}, hasCost ? '% chi phí' : '% token'),
            )),
            rows,
        ),
    );
}

function modelTable(snap: monitor.Snapshot): HTMLElement | null {
    if (snap.models.length === 0) return null;
    const rows = h('tbody');
    for (const m of snap.models) {
        rows.append(h('tr', null,
            h('th', {scope: 'row', class: 'txt', title: m.model},
                m.label || m.model,
                !m.priced && h('span', {class: 'tag tag-warn'}, 'chưa có giá'),
                m.priced && !m.verified && h('span', {class: 'tag', title: 'Giá cache của model này chưa đối chiếu được với số liệu thực tế'}, 'ước tính'),
            ),
            tokenCell(m.all),
            costCell(m.today),
            costCell(m.all),
        ));
    }
    return h('div', {class: 'block'},
        h('h2', {class: 'block-title'}, 'Theo model'),
        h('table', {class: 'grid model-table'},
            h('thead', null,
                h('tr', null,
                    h('th', {scope: 'col', rowspan: 2, class: 'txt'}, 'Model'),
                    h('th', {scope: 'col', rowspan: 2, class: 'num', title: sinceStart}, 'Token'),
                    groupHeader('Chi phí'),
                ),
                h('tr', null,
                    h('th', {scope: 'col', class: 'num sub'}, 'Hôm nay'),
                    h('th', {scope: 'col', class: 'num sub', title: sinceStart}, 'Tổng'),
                ),
            ),
            rows,
        ),
    );
}

export function ledger(snap: monitor.Snapshot): HTMLElement {
    const notes = h('div', {class: 'notes'},
        h('p', null,
            h('strong', null, 'Context'), ' là kích thước lần gọi gần nhất, đúng con số Claude Code hiển thị. ',
            h('strong', null, 'Đã tiêu'), ' là tổng token qua mọi lần gọi và là cơ sở tính chi phí.'),
        h('p', null, `Chi phí quy đổi theo giá API (bảng giá ngày ${snap.pricingAsOf}). Nếu bạn dùng gói subscription, đây không phải số tiền thực trả.`),
    );
    // Warnings are not repeated here: the meter above already carries them.

    return h('section', {class: 'ledger', id: 'ledger-detail', 'data-key': 'ledger'},
        h('div', {class: 'ledger-cols'},
            h('div', {class: 'block'},
                h('h2', {class: 'block-title'}, 'Theo project'),
                spendTable(snap),
                notes,
            ),
            h('div', {class: 'ledger-side'}, tokenMix(snap), modelTable(snap)),
        ),
    );
}

const pricingNote = 'Chi phí quy đổi theo giá API. Nếu bạn dùng gói subscription, đây không phải số tiền thực trả.';

function unpricedMark(sp: monitor.Spend): HTMLElement | null {
    if (sp.unpricedTokens <= 0) return null;
    return h('span', {class: 'unpriced', title: `Chưa gồm ${f.tokensExact(sp.unpricedTokens)} token của model chưa có giá`}, '+?');
}

// The meter is the cost summary at the top of the page, in two parts. The
// money sits beside the floor, read like a till: today's and the overall
// total at the head of two columns, the models itemised under them. Under
// both runs a band that splits the cost by kind of token and opens the full
// tables, project by project, on request.
export function meter(snap: monitor.Snapshot, view: ViewState, trend: HTMLElement | null = null): HTMLElement[] {
    const money = h('section', {class: 'money', 'data-key': 'money', 'aria-label': 'Chi phí'},
        h('h2', {class: 'money-title', title: pricingNote}, 'Chi phí'),
        h('span', {class: 'money-col'}, 'Hôm nay'),
        h('span', {class: 'money-col', title: sinceStart}, 'Tổng'),
        // The last two weeks, a bar a day, stand in the room beside the two
        // totals: the way into the history. Without them that room is empty.
        trend ?? h('span', {class: 'money-gap'}),
        h('span', {class: 'money-sum'}, f.usd(snap.todaySpend.costUsd), unpricedMark(snap.todaySpend)),
        h('span', {class: 'money-sum'}, f.usd(snap.all.costUsd), unpricedMark(snap.all)),
        !trend && h('span', {class: 'money-gap'}),
        h('span', {class: 'money-tokens', title: `${f.tokensExact(snap.todaySpend.totalTokens)} token`}, `${f.tokens(snap.todaySpend.totalTokens)} token`),
        h('span', {class: 'money-tokens', title: `${f.tokensExact(snap.all.totalTokens)} token`}, `${f.tokens(snap.all.totalTokens)} token`),
    );
    // How far today has come against the limit the user set. The fill
    // carries the state: in the accent while there is room, in the warning
    // colour near the limit, in red past it; the track is the same colour,
    // lighter.
    if (view.budget > 0) {
        const ratio = snap.todaySpend.costUsd / view.budget;
        const state = ratio >= 1 ? ' is-over' : ratio >= 0.8 ? ' is-near' : '';
        const fill = h('i');
        fill.style.width = `${Math.min(100, Math.round(ratio * 1000) / 10)}%`;
        money.append(h('span', {
            class: 'money-budget' + state,
            title: `Hôm nay ${f.usd(snap.todaySpend.costUsd)} trên ngưỡng ${f.usd(view.budget)} bạn đặt cho một ngày. Đổi ngưỡng ở trang Lịch sử.`,
        },
            h('span', {class: 'budget-bar', 'aria-hidden': 'true'}, fill),
            h('span', {class: 'budget-text'}, ratio >= 1
                ? `đã vượt ngưỡng ${f.usd(view.budget)}`
                : `${f.percent(snap.todaySpend.costUsd, view.budget)} ngưỡng ${f.usd(view.budget)}`)));
    }
    // A session that has ended still cost what it cost: it stays in both
    // totals for the rest of the day, and the till says so.
    const closedCount = snap.counts.closedSessions ?? 0;
    if (closedCount > 0) {
        money.append(h('span', {
            class: 'money-closed',
            title: `Session đã đóng nhưng có làm việc trong hôm nay vẫn được tính, đọc từ transcript còn trên đĩa. Phần của chúng: ${f.usd(snap.closedToday.costUsd)} hôm nay, ${f.usd(snap.closedAll.costUsd)} tổng.`,
        }, `Đã tính cả ${closedCount} session đã đóng hôm nay (${f.usd(snap.closedToday.costUsd)})`));
    }
    // Each model's two figures sit under the day's and the whole's totals.
    for (const m of snap.models) {
        const tokensNote = `${f.tokensExact(m.all.totalTokens)} token`;
        money.append(
            h('span', {class: 'model-name', title: `${m.model} · ${tokensNote}`},
                h('span', {class: 'model-label'}, m.label || m.model),
                !m.priced && h('span', {class: 'tag tag-warn'}, 'chưa có giá'),
                m.priced && !m.verified && h('span', {class: 'tag', title: 'Giá cache của model này chưa đối chiếu được với số liệu thực tế'}, 'ước tính'),
            ),
            h('span', {class: 'model-cost', 'aria-label': `Hôm nay ${f.usd(m.today.costUsd)}`}, f.usd(m.today.costUsd), unpricedMark(m.today)),
            h('span', {class: 'model-cost', 'aria-label': `Tổng ${f.usd(m.all.costUsd)}`}, f.usd(m.all.costUsd), unpricedMark(m.all)),
        );
    }

    const mix = mixOf(snap.all as Spend);
    const legend = h('ul', {class: 'mix-legend'});
    for (const p of mix.parts) {
        const detail = `${f.tokensExact(p.tokens)} token` + (mix.hasCost ? ` · ${f.usd(p.cost)}` : '');
        legend.append(h('li', {title: `${p.name}: ${detail}`},
            h('span', {class: `swatch seg-${p.key}`, 'aria-hidden': 'true'}),
            h('span', {class: 'mix-name'}, p.name),
            h('span', {class: 'mix-share'}, f.percent(mix.measure(p), mix.whole)),
        ));
    }

    const band = h('section', {class: 'mixband', 'data-key': 'mix', 'aria-label': 'Chi phí theo loại token'},
        h('span', {class: 'mix-title'}, mix.hasCost ? 'Chi phí theo loại token' : 'Token theo loại'),
        segbar(mix),
        legend,
        h('span', {class: 'meter-note', title: pricingNote}, `Giá API, bảng giá ${snap.pricingAsOf}`),
        h('button', {
            type: 'button', class: 'meter-more', 'data-toggle-ledger': true, 'data-fkey': 'ledger-toggle',
            'aria-expanded': String(view.ledgerOpen), 'aria-controls': view.ledgerOpen ? 'ledger-detail' : null,
        }, view.ledgerOpen ? 'Ẩn bảng chi tiết' : 'Chi tiết theo project'),
    );
    for (const w of snap.warnings) band.append(warningLine(w));
    return [money, band];
}

// ---- projects, sessions, agents ----------------------------------------------

// The pulse is a life sign: it is drawn only for something still working.
function pulse(values: number[], running: boolean, nowMs: number): HTMLElement | null {
    const total = values.reduce((a, b) => a + b, 0);
    if (total === 0) return null;
    const max = Math.max(4, ...values);
    const lastMinute = Math.floor(nowMs / 60_000) * 60_000;
    const box = h('div', {
        class: 'pulse' + (running ? ' is-live' : ''),
        role: 'img',
        'aria-label': `${total} sự kiện trong ${values.length} phút qua`,
    });
    values.forEach((v, i) => {
        const at = lastMinute - (values.length - 1 - i) * 60_000;
        const bar = h('i', {class: v > 0 ? 'on' : '', title: `${f.hourMinute(at)}: ${v} sự kiện`});
        if (v > 0) bar.style.height = `${Math.max(18, Math.round(Math.sqrt(v / max) * 100))}%`;
        box.append(bar);
    });
    return box;
}

const HIDDEN = 'đang ẩn nội dung';

function activityNode(act: monitor.Activity, nowMs: number, privacy: boolean): HTMLElement {
    const when = f.ago(act.atMs, nowMs);
    let body: Child[];
    let full: string;
    switch (act.kind) {
        case 'tool':
            // The tool's name says what kind of work it is without revealing
            // what it was run on.
            body = [h('span', {class: 'tool'}, act.tool), privacy ? '' : act.detail];
            full = act.detail && !privacy ? `${act.tool}: ${act.detail}` : act.tool;
            break;
        case 'result':
            // With the report hidden there is nothing to add to the row's own
            // status, so only the time is shown rather than the same words on
            // every finished row.
            body = privacy ? [] : [act.detail || 'Đã trả kết quả'];
            full = privacy || !act.detail ? 'Đã trả kết quả' : `Kết quả: ${act.detail}`;
            break;
        case 'text':
            body = [privacy ? `Đã trả lời (${HIDDEN})` : act.detail];
            full = privacy ? 'Đã trả lời' : act.detail;
            break;
        case 'thinking':
            body = ['Đang suy nghĩ'];
            full = 'Đang suy nghĩ';
            break;
        default:
            return h('div', {class: 'doing is-empty'}, 'Chưa có hoạt động');
    }
    return h('div', {class: 'doing', title: when ? `${full} (${when})` : full},
        h('span', {class: 'doing-text'}, ...body),
        when && h('span', {class: 'doing-when'}, when),
    );
}

export interface StatusView {
    cls: string;
    icon: IconName;
    label: string;
    title?: string;
}

export function agentStatus(a: Agent): StatusView {
    switch (a.status) {
        case 'running':
            return {cls: 'is-running', icon: 'dot', label: 'Đang chạy'};
        case 'quiet':
            if (a.awaitingTool) {
                return {
                    cls: 'is-quiet', icon: 'pause', label: `Chờ lệnh ${f.minutes(a.silentMs)}`,
                    title: 'Đã gọi một tool và chưa nhận kết quả. Lệnh chạy lâu là bình thường; nếu quá lâu thì có thể đã treo.',
                };
            }
            return {
                cls: 'is-quiet', icon: 'pause', label: `Im lặng ${f.minutes(a.silentMs)}`,
                title: 'Chưa xong nhưng không ghi gì thêm. Có thể model đang suy nghĩ lâu, hoặc đã treo.',
            };
        case 'stopped':
            return {
                cls: 'is-stopped', icon: 'square', label: 'Đã dừng',
                title: 'Không được đánh dấu là xong và đã im lặng lâu, nhiều khả năng bị ngắt giữa chừng.',
            };
        default:
            return {cls: 'is-done', icon: 'check', label: 'Xong'};
    }
}

export function sessionStatus(s: Session): StatusView {
    if (s.awaitingUser) {
        return {
            cls: 'is-quiet', icon: 'pause', label: 'Chờ bạn cho phép',
            title: 'Agent đã dừng lại để xin phép. Mở chat để trả lời.',
        };
    }
    if (s.status === 'busy') return {cls: 'is-running', icon: 'dot', label: 'Đang chạy'};
    if (s.status === 'idle') return {cls: 'is-idle', icon: 'ring', label: 'Rảnh'};
    return {cls: 'is-idle', icon: 'ring', label: s.status || 'Không rõ'};
}

export function statusNode(v: StatusView): HTMLElement {
    return h('span', {class: `status ${v.cls}`, title: v.title}, icon(v.icon), v.label);
}

// A figure in a strip. The column heading is not read out with each row, so
// the label travels with the value for assistive technology.
function num(label: string, value: string, title?: string): HTMLElement {
    return h('span', {class: 'num', 'aria-label': `${label} ${value}`, title}, value);
}

function costNum(sp: monitor.Spend): HTMLElement {
    const el = num('Chi phí', f.usd(sp.costUsd));
    if (sp.unpricedTokens > 0) {
        el.append(h('span', {class: 'unpriced', title: `Chưa gồm ${f.tokensExact(sp.unpricedTokens)} token của model chưa có giá`}, '+?'));
    }
    return el;
}

function agentStrip(a: Agent, sessionId: string, nowMs: number, view: ViewState): HTMLElement {
    const st = agentStatus(a);
    const meta = [a.type !== a.description ? a.type : '', a.modelLabel, a.phase ? `giai đoạn ${a.phase}` : '']
        .filter(Boolean).join(' · ');
    const beat = isActive(a) ? pulse(a.pulse ?? [], a.status === 'running', nowMs) : null;
    // Without a pulse to draw, the activity text takes that column too.
    const target = `${sessionId}|${a.id}`;
    const open = view.open.sessionId === sessionId && view.open.agentId === a.id;
    // The whole row opens the agent's steps; the name is the button a
    // keyboard reaches.
    return h('li', {class: `strip ${st.cls}` + (beat ? '' : ' no-pulse') + (open ? ' is-open' : ''), 'data-open-trail': target},
        h('div', {class: 'c-st'}, statusNode(st)),
        h('div', {class: 'c-name', title: [a.description, meta].filter(Boolean).join(' · ')},
            h('button', {
                type: 'button', class: 'agent-name strip-open', 'data-fkey': `trail-${target}`,
                'aria-expanded': String(open), title: 'Xem các bước agent này đã làm',
            }, a.description || a.id),
            meta && h('span', {class: 'agent-meta'}, meta),
        ),
        h('div', {class: 'c-doing'}, activityNode(a.activity, nowMs, view.privacy)),
        beat && h('div', {class: 'c-pulse'}, beat),
        h('div', {class: 'c-time'}, num('Đã chạy', f.duration(a.durationMs), `${a.toolUses} lần gọi tool`)),
        h('div', {class: 'c-ctx'}, num('Context', f.tokens(a.contextTokens), `${f.tokensExact(a.contextTokens)} token trong context ở lần gọi gần nhất`)),
        h('div', {class: 'c-spent'}, num('Đã tiêu', f.tokens(a.all.totalTokens), `${f.tokensExact(a.all.totalTokens)} token qua ${a.all.tokens.requests} lần gọi API`)),
        h('div', {class: 'c-cost'}, costNum(a.all)),
    );
}

// One heading row for the whole board. It stays in view under the top bar, so
// the figures keep their names however far down the list is scrolled. Over
// the pulses it is a time axis: every pulse runs from 30 minutes ago on the
// left to now on the right, so one agent's beats can be read against another's.
function stripHeader(showPulse: boolean): HTMLElement {
    return h('div', {class: 'strip-cols board-cols', 'data-key': `cols:${showPulse ? 'pulse' : 'flat'}`, 'aria-hidden': 'true'},
        h('span', {class: 'c-st'}, 'Trạng thái'),
        h('span', {class: 'c-name'}, 'Session và agent'),
        h('span', {class: 'c-doing'}, 'Hoạt động gần nhất'),
        showPulse
            ? h('span', {class: 'c-pulse axis', title: 'Nhịp: số sự kiện mỗi phút trong 30 phút qua'},
                h('span', null, '−30 phút'), h('span', null, 'bây giờ'))
            : h('span', {class: 'c-pulse'}),
        h('span', {class: 'c-time num'}, 'Đã chạy'),
        h('span', {class: 'c-ctx num', title: 'Kích thước context ở lần gọi gần nhất. Đây là con số Claude Code hiển thị cho mỗi agent.'}, 'Context'),
        h('span', {class: 'c-spent num', title: 'Tổng token đã xử lý qua mọi lần gọi API. Mỗi lần gọi đều gửi lại toàn bộ context, nên số này lớn hơn context nhiều lần.'}, 'Đã tiêu'),
        h('span', {class: 'c-cost num'}, 'Chi phí'),
    );
}

// The session's own row leads its block and sits on the same columns as its
// agents, so its figures read as the block's totals.
function sessionStrip(s: Session, nowMs: number, view: ViewState, beat: HTMLElement | null): HTMLElement {
    const st = sessionStatus(s);
    const title = s.title || s.name || s.id;
    const meta = [
        s.modelLabel,
        s.gitBranch && `nhánh ${s.gitBranch}`,
        s.name && s.name !== title ? s.name : '',
        s.today.costUsd > 0 && `hôm nay ${f.usd(s.today.costUsd)}`,
    ].filter(Boolean).join(' · ');

    const open = view.open.sessionId === s.id && view.open.agentId === '';
    // A session of the app's own opens its chat; any other opens the steps
    // of its main thread, to be read only.
    const strip = h('li', {
        class: `strip lead-strip ${st.cls}` + (open ? ' is-open' : ''),
        'data-open-chat': s.managed ? s.id : null,
        'data-open-trail': s.managed || !s.hasTranscript ? null : `${s.id}|`,
    },
        h('div', {class: 'c-st'}, statusNode(st)),
        h('div', {class: 'c-title'},
            h('h3', {class: 'session-title', title},
                s.managed || !s.hasTranscript
                    ? title
                    : h('button', {
                        type: 'button', class: 'strip-open', 'data-fkey': `trail-${s.id}|`,
                        'aria-expanded': String(open), title: 'Xem các bước của luồng chính',
                    }, title)),
            meta && h('span', {class: 'session-meta'}, meta),
            s.duplicate && h('span', {class: 'tag', title: 'Session này đang mở ở hai nơi. Số liệu chỉ được cộng vào tổng một lần.'}, 'mở ở hai nơi'),
            // Only a session this app started can be talked to from here.
            s.managed && h('button', {
                type: 'button', class: 'act', 'data-open-chat': s.id, 'data-fkey': `chat-${s.id}`,
                title: 'Mở khung chat của session này',
            }, 'Mở chat'),
        ),
        beat && h('div', {class: 'c-pulse'}, beat),
        h('div', {class: 'c-time'}, num('Đã chạy', s.startedAtMs ? f.duration(nowMs - s.startedAtMs) : '', 'Từ lúc process của session này khởi động')),
        h('div', {class: 'c-ctx'}, num('Context', f.tokens(s.contextTokens), `${f.tokensExact(s.contextTokens)} token trong context của luồng chính`)),
        h('div', {class: 'c-spent'}, num('Đã tiêu', f.tokens(s.all.totalTokens), `Luồng chính ${f.tokensExact(s.own.totalTokens)} token, tính cả agent ${f.tokensExact(s.all.totalTokens)} token`)),
        h('div', {class: 'c-cost'}, costNum(s.all)),
    );

    if (!s.hasTranscript) {
        strip.append(
            h('span', {class: 'c-dlabel'}, 'Luồng chính'),
            h('div', {class: 'c-doing'}, h('div', {class: 'doing is-empty'}, 'Session vừa mở, chưa có hội thoại nào')),
        );
        return strip;
    }
    if (s.lastPrompt) {
        strip.append(
            h('span', {class: 'c-plabel'}, 'Yêu cầu gần nhất'),
            view.privacy
                ? h('div', {class: 'c-prompt is-hidden'}, `(${HIDDEN})`)
                : h('div', {class: 'c-prompt', title: s.lastPrompt}, s.lastPrompt),
        );
    }
    strip.append(
        h('span', {class: 'c-dlabel'}, 'Luồng chính'),
        h('div', {class: 'c-doing'}, activityNode(s.activity, nowMs, view.privacy)),
    );
    return strip;
}

function sessionBlock(s: Session, nowMs: number, view: ViewState): HTMLElement {
    const sessionBeat = s.status === 'busy' ? pulse(s.pulse ?? [], true, nowMs) : null;
    const active = s.agents.filter(isActive);
    const ended = s.agents.filter(a => !isActive(a));
    // Under a filter the list is already short: nothing of it is folded away.
    const open = view.expanded.has(s.id) || view.filter.trim() !== '';
    const shown = view.hideEnded ? [] : open ? ended : ended.slice(0, ENDED_SHOWN);

    const list = h('ul', {class: 'strips'}, sessionStrip(s, nowMs, view, sessionBeat));
    for (const a of active) list.append(agentStrip(a, s.id, nowMs, view));
    for (const a of shown) list.append(agentStrip(a, s.id, nowMs, view));

    const block = h('article', {class: 'session', 'data-key': `s:${s.id}:${s.pid}`}, list);

    if (view.hideEnded && ended.length > 0) {
        block.append(h('p', {class: 'fold-note'}, `${ended.length} agent đã kết thúc đang được ẩn.`));
    } else if (ended.length > ENDED_SHOWN) {
        block.append(h('button', {
            type: 'button', class: 'fold', 'data-toggle-session': s.id, 'data-fkey': `fold-${s.id}`,
            'aria-expanded': String(open),
        }, open ? 'Thu gọn danh sách' : `Hiện thêm ${ended.length - shown.length} agent đã kết thúc`));
    }
    return block;
}

// closedBlock lists the sessions of a project that have ended but were at
// work today. They have nothing more to show than what they were and what
// they cost, which is why they are here: the day's total includes them.
function closedBlock(closed: monitor.ClosedSession[], nowMs: number, view: ViewState): HTMLElement {
    const list = h('ul', {class: 'strips'});
    for (const c of closed) {
        const target = `${c.id}|`;
        const open = view.open.sessionId === c.id && view.open.agentId === '';
        const meta = [
            c.modelLabel,
            c.gitBranch && `nhánh ${c.gitBranch}`,
            c.agents > 0 && `${c.agents} agent`,
            c.today.costUsd > 0 && `hôm nay ${f.usd(c.today.costUsd)}`,
        ].filter(Boolean).join(' · ');
        // The row opens what the session did, to be read, and from there it
        // can be carried on.
        list.append(h('li', {class: 'strip is-closed no-pulse' + (open ? ' is-open' : ''), 'data-open-trail': target},
            h('div', {class: 'c-st'}, statusNode({
                cls: 'is-done', icon: 'square', label: 'Đã đóng',
                title: 'Session này đã kết thúc. Chi phí của nó vẫn nằm trong tổng của hôm nay.',
            })),
            h('div', {class: 'c-name', title: c.title},
                h('button', {
                    type: 'button', class: 'agent-name strip-open', 'data-fkey': `trail-${target}`,
                    'aria-expanded': String(open), title: 'Xem các bước của session này, và mở lại nó nếu muốn',
                }, c.title || 'Session chưa có tiêu đề'),
                meta && h('span', {class: 'agent-meta'}, meta),
            ),
            h('div', {class: 'c-doing'}, h('div', {class: 'doing'},
                h('span', {class: 'doing-text'}, 'Hoạt động cuối'),
                c.endedAtMs > 0 && h('span', {class: 'doing-when', title: f.clock(c.endedAtMs)}, f.ago(c.endedAtMs, nowMs)))),
            h('div', {class: 'c-time'}),
            h('div', {class: 'c-ctx'}),
            h('div', {class: 'c-spent'}, num('Đã tiêu', f.tokens(c.all.totalTokens), `${f.tokensExact(c.all.totalTokens)} token, tính cả agent của nó`)),
            h('div', {class: 'c-cost'}, costNum(c.all)),
        ));
    }
    return h('div', {class: 'closed', 'data-key': 'closed'},
        h('p', {class: 'closed-head'}, closed.length > 1 ? `${closed.length} session đã đóng hôm nay` : 'Đã đóng hôm nay'),
        list);
}

// liveFirst keeps the given order within each group, so rows only move when
// something starts or stops working.
function liveFirst<T>(items: T[], isLive: (item: T) => boolean): T[] {
    return [...items.filter(isLive), ...items.filter(item => !isLive(item))];
}

export function bays(snap: monitor.Snapshot, view: ViewState): HTMLElement {
    const nowMs = snap.generatedAtMs;
    const beating = snap.projects.some(p => p.sessions.some(s =>
        (s.status === 'busy' && (s.pulse ?? []).some(v => v > 0)) || s.agents.some(a => isActive(a) && (a.pulse ?? []).some(v => v > 0))));
    const wrap = h('div', {class: 'bays', 'data-key': 'bays', 'data-container': true}, stripHeader(beating));
    const hit = carries(view.filter);
    const filtering = view.filter.trim() !== '';
    let listed = 0;
    for (const whole of liveFirst(snap.projects, project => project.sessions.some(sessionIsLive))) {
        // A project whose name carries the filter is listed whole. Of any
        // other, the sessions that carry it, or have an agent that does.
        let p = whole;
        if (filtering && !hit(whole.name, whole.path)) {
            const sessions: monitor.Session[] = [];
            for (const s of whole.sessions) {
                if (hit(s.title, s.name, s.gitBranch, s.modelLabel)) {
                    sessions.push(s);
                    continue;
                }
                const agents = s.agents.filter(a => hit(a.description, a.type, a.modelLabel, a.phase));
                if (agents.length > 0) sessions.push({...s, agents} as monitor.Session);
            }
            const closed = closedOf(whole).filter(c => hit(c.title, c.gitBranch, c.modelLabel));
            if (sessions.length === 0 && closed.length === 0) continue;
            p = {...whole, sessions, closed} as monitor.Project;
        }
        listed++;
        // A project whose session runs inside VS Code can be jumped to.
        const inEditor = p.sessions.some(s => s.entrypoint.includes('vscode'));
        // A project with nothing at work keeps a quieter heading. The mark is
        // on the heading, not the bay: the bay is patched in place and keeps
        // its own attributes, the heading is redrawn when it changes.
        const idle = !p.sessions.some(sessionIsLive);
        const bay = h('section', {class: 'bay', 'data-key': `bay:${p.path}`, 'data-container': true},
            h('header', {class: 'bay-head' + (idle ? ' is-idle' : '') + (view.open.projectPath === p.path ? ' is-open' : ''), 'data-key': 'head'},
                h('h2', {class: 'bay-name', title: p.path},
                    inEditor
                        ? h('button', {
                            type: 'button', class: 'bay-open', 'data-open-project': p.path, 'data-fkey': `open-${p.path}`,
                            title: `Chuyển sang cửa sổ VS Code đang mở ${p.path}`,
                        }, h('span', {class: 'bay-open-name'}, p.name), h('span', {class: 'bay-open-hint'}, 'Mở VS Code'))
                        : p.name),
                p.sessions.length > 1 && h('span', {class: 'bay-sum'},
                    `${p.sessions.length} session · ${f.tokens(p.all.totalTokens)} token · ${f.usd(p.all.costUsd)}`),
                h('span', {class: 'bay-acts'},
                    h('button', {
                        type: 'button', class: 'act', 'data-new-session': p.path, 'data-project-name': p.name,
                        'data-fkey': `new-${p.path}`, title: `Mở một session Claude Code mới tại ${p.path} và chat ngay trong app`,
                    }, 'Session mới'),
                    (p as Project).pinned && h('button', {
                        type: 'button', class: 'act act-quiet', 'data-remove-project': p.path, 'data-fkey': `rm-${p.path}`,
                        title: 'Bỏ project này khỏi danh sách đã thêm. Thư mục và các session không bị ảnh hưởng.',
                    }, 'Bỏ khỏi danh sách'),
                ),
            ),
        );
        const closed = closedOf(p);
        if (p.sessions.length === 0 && closed.length === 0) {
            bay.append(h('p', {class: 'bay-empty', 'data-key': 'empty'}, 'Chưa có session nào đang chạy ở project này.'));
        }
        for (const s of liveFirst(p.sessions, sessionIsLive)) bay.append(sessionBlock(s as Session, nowMs, view));
        if (closed.length > 0) bay.append(closedBlock(closed, nowMs, view));
        wrap.append(bay);
    }
    if (filtering && listed === 0) {
        wrap.append(h('p', {class: 'bay-empty filter-none', 'data-key': 'filter-none'},
            `Không có project, session hay agent nào mang chữ "${view.filter.trim()}". Các con số ở trên vẫn tính mọi thứ.`));
    }
    return wrap;
}

// nothingRunning is the whole page when no Claude Code session is open.
export function nothingRunning(snap: monitor.Snapshot): HTMLElement {
    const box = h('div', {class: 'blank', 'data-key': 'blank'},
        h('p', {class: 'blank-title'}, 'Chưa mở session Claude Code nào'),
        h('p', null, 'Mở Claude Code trong một project bất kỳ, trang này tự cập nhật sau vài giây. Hoặc thêm một project để mở session ngay tại đây.'),
        h('p', null, h('button', {type: 'button', class: 'act', 'data-add-project': true, 'data-fkey': 'add-project-blank'}, 'Thêm project')),
    );
    for (const w of snap.warnings) box.append(warningLine(w));
    return box;
}
