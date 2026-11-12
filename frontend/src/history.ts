import {monitor} from '../wailsjs/go/models';
import * as f from './format';
import {h} from './render';

// The history: what each day cost, for as far back as the app has kept count.
//
// It is the board's other time scale. The board says who is at work now; this
// page says what the days added up to. The chart is columns on one baseline,
// a day each, split by project, by model or by kind of token. Under it every
// project and every model is a strip with its own run of days on the same
// time axis as the chart, the way the board sets every agent's pulse on one
// axis: when a project was at work can be read against the others.
//
// Colour is identity here. The four data colours go, in their fixed order, to
// the four largest of whatever the chart is split by, ranked over everything
// that was fetched and not over the stretch on screen, so changing the
// stretch does not repaint a project. The rest is one neutral "Khác".

export type Split = 'project' | 'model' | 'token';
export type Range = 7 | 30 | 90;

export const RANGES: Range[] = [7, 30, 90];
/** How many days are fetched: the longest stretch and the one before it. */
export const FETCH_DAYS = 180;

export interface HistState {
    range: Range;
    split: Split;
    /** The day picked on the chart, or '' for the whole stretch. */
    day: string;
    /** Set when the chart is to be drawn as new, so its columns rise once. */
    fresh: boolean;
    tableOpen: boolean;
}

type Day = monitor.HistoryDay;
type Part = monitor.HistoryPart;

const SLOTS = ['s1', 's2', 's3', 's4'] as const;
const OTHER = 'other';

interface Series {
    key: string;
    name: string;
    title: string;
    /** 's1'..'s4', or 'other' for everything past the fourth. */
    slot: string;
}

const TOKEN_SERIES: Series[] = [
    {key: 'cacheRead', name: 'Cache đọc', title: '', slot: 's1'},
    {key: 'cacheWrite', name: 'Cache ghi', title: '', slot: 's2'},
    {key: 'output', name: 'Output', title: '', slot: 's3'},
    {key: 'input', name: 'Input', title: '', slot: 's4'},
];

function partsOf(d: Day, split: Split): Part[] {
    return split === 'model' ? d.models ?? [] : d.projects ?? [];
}

// seriesOf names what the chart is split into. For projects and models it is
// the four that cost most over all the days fetched, then everything else.
function seriesOf(all: Day[], split: Split): Series[] {
    if (split === 'token') return TOKEN_SERIES;
    const sums = new Map<string, { name: string; path: string; cost: number; tokens: number }>();
    for (const d of all) {
        for (const p of partsOf(d, split)) {
            const cur = sums.get(p.key) ?? {name: p.name, path: p.path, cost: 0, tokens: 0};
            cur.cost += p.costUsd;
            cur.tokens += p.tokens;
            sums.set(p.key, cur);
        }
    }
    const ranked = Array.from(sums.entries())
        .sort((a, b) => b[1].cost - a[1].cost || b[1].tokens - a[1].tokens || (a[0] < b[0] ? -1 : 1));
    const out: Series[] = ranked.slice(0, SLOTS.length).map(([key, v], i) => ({key, name: v.name || key, title: v.path || key, slot: SLOTS[i]}));
    if (ranked.length > SLOTS.length) {
        const rest = ranked.length - SLOTS.length;
        out.push({key: OTHER, name: `Khác (${rest})`, title: `${rest} ${split === 'model' ? 'model' : 'project'} còn lại, gộp chung`, slot: OTHER});
    }
    return out;
}

// valuesOf is a day's cost split along the series, in their order.
function valuesOf(d: Day, split: Split, series: Series[]): number[] {
    if (split === 'token') {
        const c = d.spend.costParts;
        // Web searches are not tokens; what they cost goes with the output
        // they belong to, so the parts still add up to the day.
        return [c.cacheRead, c.cacheWrite, c.output + c.webSearch, c.input];
    }
    const at = new Map(series.map((s, i) => [s.key, i]));
    const out = series.map(() => 0);
    const rest = at.get(OTHER);
    for (const p of partsOf(d, split)) {
        const i = at.get(p.key) ?? rest;
        if (i !== undefined) out[i] += p.costUsd;
    }
    return out;
}

const sum = (xs: number[]) => xs.reduce((a, b) => a + b, 0);
const costOf = (days: Day[]) => sum(days.map(d => d.spend.costUsd));

// niceMax rounds a maximum up to a figure an axis can be divided at, and says
// in what step.
function niceAxis(max: number): { top: number; step: number } {
    if (max <= 0) return {top: 1, step: 0.5};
    const rough = max / 3;
    const pow = Math.pow(10, Math.floor(Math.log10(rough)));
    const step = [1, 2, 2.5, 5, 10].map(m => m * pow).find(s => s >= rough) ?? 10 * pow;
    return {top: Math.ceil(max / step - 1e-9) * step, step};
}

function button(label: string, attrs: Record<string, string | boolean | null>, pressed: boolean): HTMLElement {
    return h('button', {type: 'button', class: 'seg-btn', 'aria-pressed': String(pressed), ...attrs}, label);
}

// ---- the chart -----------------------------------------------------------------

function tickLabel(day: string, i: number, days: Day[]): string {
    const n = days.length;
    if (n <= 14) return f.dayLabel(day);
    // Longer stretches name their Mondays, or every other one when crowded.
    if (f.weekday(day) !== 1) return '';
    if (n > 45) {
        const mondays = days.slice(0, i + 1).filter(d => f.weekday(d.day) === 1).length;
        if (mondays % 2 === 0) return '';
    }
    return f.dayShort(day);
}

function chart(days: Day[], split: Split, series: Series[], st: HistState, today: string, budget: number): HTMLElement {
    const totals = days.map(d => d.spend.costUsd);
    const peak = Math.max(0, ...totals);
    // The limit is drawn when it is within sight of the columns; far above
    // them it would flatten the chart to say one thing.
    const showBudget = budget > 0 && budget <= Math.max(peak * 2.5, 1);
    const {top, step} = niceAxis(Math.max(peak, showBudget ? budget : 0));

    const grid = h('div', {class: 'hc-grid', 'aria-hidden': 'true'});
    for (let v = 0; v <= top + 1e-9; v += step) {
        const line = h('div', {class: 'hc-line' + (v === 0 ? ' is-base' : '')}, h('span', {class: 'hc-tick'}, f.usdAxis(v)));
        line.style.bottom = `${(v / top) * 100}%`;
        grid.append(line);
    }
    if (showBudget) {
        const line = h('div', {class: 'hc-line is-budget'}, h('span', {class: 'hc-budget'}, `ngưỡng ${f.usd(budget)}`));
        line.style.bottom = `${(budget / top) * 100}%`;
        grid.append(line);
    }

    // The box is kept from one drawing to the next and only its columns are
    // replaced, so whatever changes must be said on the columns themselves.
    const cols = h('div', {class: 'hc-cols', 'data-key': 'cols', 'data-container': true});
    cols.style.setProperty('--n', String(days.length));
    days.forEach((d, i) => {
        const values = valuesOf(d, split, series);
        const stack = h('span', {class: 'hc-stack' + (st.fresh ? ' is-rising' : '')});
        stack.style.height = `${(d.spend.costUsd / top) * 100}%`;
        // The stagger makes the columns rise left to right, in order of time.
        if (st.fresh) stack.style.animationDelay = `${Math.round((i / Math.max(1, days.length)) * 260)}ms`;
        values.forEach((v, k) => {
            if (v <= 0 || d.spend.costUsd <= 0) return;
            const seg = h('i', {class: `hc-seg seg-${series[k].slot}`});
            seg.style.flexGrow = String(Math.max(1, Math.round((v / d.spend.costUsd) * 10_000)));
            stack.append(seg);
        });
        const weekend = f.weekday(d.day) === 0 || f.weekday(d.day) === 6;
        // The day that is picked is named, whatever day of the week it is.
        const label = d.day === st.day ? f.dayShort(d.day) : tickLabel(d.day, i, days);
        const pick = d.day === st.day ? ' is-picked' : st.day ? ' is-dimmed' : '';
        cols.append(h('button', {
            type: 'button',
            class: 'hc-col' + (d.day === today ? ' is-today' : '') + (weekend ? ' is-weekend' : '') + pick,
            'data-key': `d:${d.day}`,
            'data-hist-day': d.day,
            'data-fkey': `hist-${d.day}`,
            // One stop for the keyboard; the arrow keys walk the days.
            tabindex: (st.day ? d.day === st.day : i === days.length - 1) ? 0 : -1,
            'aria-pressed': String(d.day === st.day),
            'aria-label': `${f.dayLong(d.day)}: ${f.usd(d.spend.costUsd)}, ${d.sessions} session`,
        },
            h('span', {class: 'hc-plot'}, stack),
            h('span', {class: 'hc-x'}, label),
        ));
    });

    return h('div', {class: 'hc', 'data-key': `chart:${st.range}:${split}`, 'data-container': true},
        h('div', {class: 'hc-frame', 'data-key': `frame:${top}:${showBudget ? budget : 0}`, 'data-container': true}, grid, cols));
}

function legend(series: Series[], split: Split): HTMLElement {
    const list = h('ul', {class: 'mix-legend hc-legend', 'data-key': `legend:${split}:${series.map(s => s.key).join('|')}`});
    for (const s of series) {
        list.append(h('li', {title: s.title || s.name},
            h('span', {class: `swatch seg-${s.slot}`, 'aria-hidden': 'true'}),
            h('span', {class: 'mix-name'}, s.name)));
    }
    return list;
}

// ---- strips: one per project, one per model ---------------------------------------

interface Row {
    key: string;
    name: string;
    title: string;
    cost: number;
    tokens: number;
    unpriced: number;
    perDay: number[];
}

function rowsOf(days: Day[], split: 'project' | 'model', only: string): Row[] {
    const rows = new Map<string, Row>();
    days.forEach((d, i) => {
        for (const p of partsOf(d, split)) {
            let r = rows.get(p.key);
            if (!r) {
                r = {key: p.key, name: p.name || p.key, title: p.path || p.key, cost: 0, tokens: 0, unpriced: 0, perDay: days.map(() => 0)};
                rows.set(p.key, r);
            }
            r.perDay[i] += p.costUsd;
            if (only && d.day !== only) continue;
            r.cost += p.costUsd;
            r.tokens += p.tokens;
            r.unpriced += p.unpricedTokens;
        }
    });
    return Array.from(rows.values())
        .filter(r => r.tokens > 0)
        .sort((a, b) => b.cost - a.cost || b.tokens - a.tokens || (a.key < b.key ? -1 : 1));
}

function spark(values: number[], days: Day[], picked: string): HTMLElement {
    const max = Math.max(...values, 0);
    const box = h('span', {class: 'hspark', 'aria-hidden': 'true'});
    values.forEach((v, i) => {
        const bar = h('i', {class: (v > 0 ? 'on' : '') + (days[i].day === picked ? ' is-picked' : ''), title: `${f.dayLabel(days[i].day)}: ${f.usd(v)}`});
        if (v > 0 && max > 0) bar.style.height = `${Math.max(12, Math.round((v / max) * 100))}%`;
        box.append(bar);
    });
    return box;
}

function strips(title: string, what: string, rows: Row[], days: Day[], series: Series[], colored: boolean, st: HistState, whole: number): HTMLElement {
    const slotOf = new Map(series.map(s => [s.key, s.slot]));
    const list = h('ul', {class: 'hrows'});
    for (const r of rows) {
        const slot = colored ? slotOf.get(r.key) ?? OTHER : '';
        list.append(h('li', {class: 'hrow'},
            h('span', {class: 'hrow-name', title: r.title},
                slot && h('span', {class: `swatch seg-${slot}`, 'aria-hidden': 'true'}),
                h('span', {class: 'hrow-label'}, r.name)),
            spark(r.perDay, days, st.day),
            h('span', {class: 'num hrow-tokens', title: `${f.tokensExact(r.tokens)} token`}, f.tokens(r.tokens)),
            h('span', {class: 'num hrow-cost'}, f.usd(r.cost),
                r.unpriced > 0 && h('span', {class: 'unpriced', title: `Chưa gồm ${f.tokensExact(r.unpriced)} token của model chưa có giá`}, '+?')),
            h('span', {class: 'num hrow-share'}, f.percent(r.cost, whole)),
        ));
    }
    if (rows.length === 0) list.append(h('li', {class: 'hrow-none'}, `Không có ${what} nào trong ${st.day ? 'ngày này' : 'khoảng này'}.`));
    return h('div', {class: 'block', 'data-key': `strips:${what}`},
        h('h2', {class: 'block-title'}, title),
        h('div', {class: 'hrow hrow-head', 'aria-hidden': 'true'},
            h('span', null, what === 'project' ? 'Project' : 'Model'),
            h('span', {class: 'hspark-axis', title: 'Chi phí mỗi ngày trên cùng trục thời gian với biểu đồ. Mỗi dòng có thang riêng: cột cao nhất là ngày tốn nhất của chính dòng đó.'},
                h('span', null, f.dayShort(days[0].day)), h('span', null, f.dayShort(days[days.length - 1].day))),
            h('span', {class: 'num'}, 'Token'),
            h('span', {class: 'num'}, 'Chi phí'),
            h('span', {class: 'num'}, 'Tỷ trọng')),
        list);
}

// The same days as a table: every figure the chart shows, without a pointer.
function dayTable(days: Day[], st: HistState): HTMLElement {
    const body = h('tbody');
    for (const d of [...days].reverse()) {
        if (d.spend.totalTokens === 0) continue;
        body.append(h('tr', null,
            h('th', {scope: 'row', class: 'txt'}, f.dayLabel(d.day)),
            h('td', {class: 'num'}, String(d.sessions)),
            h('td', {class: 'num', title: `${f.tokensExact(d.spend.totalTokens)} token`}, f.tokens(d.spend.totalTokens)),
            h('td', {class: 'num'}, f.usd(d.spend.costUsd)),
            h('td', {class: 'txt hday-top'}, (d.projects ?? []).slice(0, 3).map(p => `${p.name} ${f.usd(p.costUsd)}`).join(' · ')),
        ));
    }
    return h('div', {class: 'block hday', 'data-key': 'days'},
        h('button', {
            type: 'button', class: 'meter-more', 'data-hist-table': true, 'data-fkey': 'hist-table',
            'aria-expanded': String(st.tableOpen),
        }, st.tableOpen ? 'Ẩn bảng theo ngày' : 'Xem từng ngày dạng bảng'),
        st.tableOpen && h('table', {class: 'grid hday-table'},
            h('thead', null, h('tr', null,
                h('th', {scope: 'col', class: 'txt'}, 'Ngày'),
                h('th', {scope: 'col', class: 'num'}, 'Session'),
                h('th', {scope: 'col', class: 'num'}, 'Token'),
                h('th', {scope: 'col', class: 'num'}, 'Chi phí'),
                h('th', {scope: 'col', class: 'txt hday-top'}, 'Project tốn nhất'))),
            body));
}

// ---- the page --------------------------------------------------------------------

function mondayOf(day: string): string {
    const d = f.dayDate(day);
    d.setDate(d.getDate() - ((d.getDay() + 6) % 7));
    return iso(d);
}

function iso(d: Date): string {
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

function shift(day: string, by: number): string {
    const d = f.dayDate(day);
    d.setDate(d.getDate() + by);
    return iso(d);
}

const between = (all: Day[], from: string, to: string) => all.filter(d => d.day >= from && d.day <= to);

// The till of the history page: today, this week and this month, each beside
// the period before it, so a figure is never read without its measure.
function till(all: Day[], today: string, firstDay: string, budget: number): HTMLElement {
    const monday = mondayOf(today);
    const monthStart = today.slice(0, 8) + '01';
    const lastMonthEnd = shift(monthStart, -1);
    const lastMonthStart = lastMonthEnd.slice(0, 8) + '01';
    // A week or a month that is not over is measured against the same days
    // of the one before: Monday to Wednesday against Monday to Wednesday.
    // Against the whole of it, every unfinished period would look cheap.
    const sameDayLastMonth = lastMonthStart.slice(0, 8) + today.slice(8, 10);
    const lastMonthTo = sameDayLastMonth > lastMonthEnd ? lastMonthEnd : sameDayLastMonth;
    const todayCost = costOf(between(all, today, today));

    // A period the history does not reach back over has no figure to give.
    const known = (from: string) => firstDay !== '' && firstDay <= from && all.length > 0 && all[0].day <= from;
    const line = (label: string, cost: number, beforeLabel: string, before: number | null) => [
        h('span', {class: 'money-col hm-label'}, label),
        h('span', {class: 'hm-sum'}, f.usd(cost)),
        h('span', {class: 'hm-before'}, before === null ? '' : `${beforeLabel} ${f.usd(before)}`),
    ];
    const figures = h('div', {class: 'hm-grid', 'data-key': 'figures'},
        h('h2', {class: 'money-title', title: 'Chi phí quy đổi theo giá API. Nếu bạn dùng gói subscription, đây không phải số tiền thực trả.'}, 'Chi phí theo kỳ'),
        ...line('Hôm nay', todayCost, 'hôm qua', known(shift(today, -1)) ? costOf(between(all, shift(today, -1), shift(today, -1))) : null),
        ...line('Tuần này', costOf(between(all, monday, today)), 'cùng kỳ tuần trước',
            known(shift(monday, -7)) ? costOf(between(all, shift(monday, -7), shift(today, -7))) : null),
        ...line(`Tháng ${Number(today.slice(5, 7))}`, costOf(between(all, monthStart, today)), `cùng kỳ ${f.monthLabel(lastMonthStart)}`,
            known(lastMonthStart) ? costOf(between(all, lastMonthStart, lastMonthTo)) : null),
    );

    // The daily limit is set here, where the days are; the board's till
    // shows how far today has come against it. It is a node of its own,
    // apart from the figures: those are redrawn whenever a cost moves, and
    // that must not take the box away from someone typing in it.
    const input = h('input', {
        type: 'number', class: 'hm-budget-input', id: 'hist-budget', min: 0, max: 1000000, step: 1, inputmode: 'decimal',
        placeholder: 'không đặt', 'data-hist-budget': true, 'data-fkey': 'hist-budget',
        title: 'Windows báo một lần khi chi phí trong ngày vượt số này (cần bật "Báo khi xong"). Để trống là không đặt ngưỡng.',
    });
    if (budget > 0) input.setAttribute('value', String(budget));
    const limit = h('label', {class: 'hm-budget', for: 'hist-budget', 'data-key': 'budget'},
        h('span', null, 'Báo khi một ngày vượt'),
        h('span', {class: 'hm-budget-field'}, h('span', {'aria-hidden': 'true'}, '$'), input));
    return h('section', {class: 'money hmoney', 'data-key': 'till', 'data-container': true, 'aria-label': 'Chi phí theo kỳ'}, figures, limit);
}

export function historyView(hist: monitor.History | null, st: HistState, problem: string, budget: number): HTMLElement {
    if (!hist) {
        return h('div', {class: 'blank', 'data-key': 'hist-blank'},
            h('p', {class: 'blank-title'}, problem ? 'Không đọc được lịch sử' : 'Đang mở sổ chi phí'),
            problem && h('p', null, problem));
    }
    const all = hist.days ?? [];
    const scan = hist.scan;
    const scanning = scan.running;
    const scanNote = scanning && h('p', {class: 'hist-scan', 'data-key': 'scan', role: 'status'},
        h('span', {class: 'lamp', 'aria-hidden': 'true'}),
        scan.total > 0
            ? `Đang đọc transcript của những ngày trước: ${scan.done} / ${scan.total} session. Số liệu đầy dần lên.`
            : 'Đang tìm transcript của những ngày trước.');
    // A fetch that failed leaves the last good figures on screen, and says so.
    const stale = problem !== '' && h('p', {class: 'warning hist-problem', 'data-key': 'stale'},
        h('span', {class: 'warning-mark', 'aria-hidden': 'true'}, '!'),
        `Không cập nhật được lịch sử, đang hiện số liệu đã đọc trước đó: ${problem}`);

    if (hist.firstDay === '' || all.length === 0) {
        return h('div', {class: 'history', 'data-key': 'history', 'data-container': true},
            h('div', {class: 'blank', 'data-key': 'hist-blank'},
                h('p', {class: 'blank-title'}, scanning ? 'Đang dựng lịch sử' : 'Chưa có lịch sử'),
                h('p', null, scanning
                    ? 'App đang đọc transcript mà Claude Code còn giữ trên đĩa. Lần đầu mất vài giây tới vài phút tuỳ số session.'
                    : 'Chưa có session nào để tính. Mỗi ngày có session chạy sẽ thành một cột ở đây.')),
            scanNote);
    }

    const days = all.slice(-st.range);
    const prev = all.slice(-2 * st.range, -st.range);
    const series = seriesOf(all, st.split);
    const total = costOf(days);
    const picked = st.day ? days.find(d => d.day === st.day) : undefined;
    const worked = days.filter(d => d.spend.totalTokens > 0);
    const busiest = worked.reduce<Day | null>((a, d) => (a === null || d.spend.costUsd > a.spend.costUsd ? d : a), null);
    const unpriced = sum((picked ? [picked] : days).map(d => d.spend.unpricedTokens));

    // The stretch before this one is a fair measure only when the history
    // reaches back over all of it.
    const prevKnown = prev.length === st.range && hist.firstDay <= prev[0].day;
    const moved = prevKnown ? f.change(total, costOf(prev)) : '';

    const filters = h('div', {class: 'hist-filters', 'data-key': `filters:${st.range}:${st.split}`},
        h('div', {class: 'seg-group', role: 'group', 'aria-label': 'Khoảng thời gian'},
            ...RANGES.map(r => button(`${r} ngày`, {'data-hist-range': String(r), 'data-fkey': `hist-range-${r}`}, st.range === r))),
        h('div', {class: 'seg-group', role: 'group', 'aria-label': 'Tô màu biểu đồ theo'},
            h('span', {class: 'seg-label'}, 'Tô màu theo'),
            button('Project', {'data-hist-split': 'project', 'data-fkey': 'hist-split-project'}, st.split === 'project'),
            button('Model', {'data-hist-split': 'model', 'data-fkey': 'hist-split-model'}, st.split === 'model'),
            button('Loại token', {'data-hist-split': 'token', 'data-fkey': 'hist-split-token'}, st.split === 'token')),
        h('button', {
            type: 'button', class: 'act hist-export', 'data-hist-export': true, 'data-fkey': 'hist-export',
            title: `Ghi ${st.range} ngày này ra file CSV, mỗi dòng là một ngày, một project và một model`,
        }, 'Xuất CSV'),
    );

    const headline = picked
        ? h('h1', {class: 'headline'},
            h('span', null, f.usd(picked.spend.costUsd)),
            h('span', {class: 'headline-sub'}, f.dayLong(picked.day)),
            h('button', {type: 'button', class: 'act', 'data-hist-day': picked.day, 'data-fkey': 'hist-unpick'}, 'Bỏ chọn ngày'))
        : h('h1', {class: 'headline'},
            h('span', null, f.usd(total)),
            h('span', {class: 'headline-sub'}, `trong ${st.range} ngày qua`));
    if (unpriced > 0) {
        headline.firstElementChild!.append(h('span', {class: 'unpriced', title: `Chưa gồm ${f.tokensExact(unpriced)} token của model chưa có giá`}, '+?'));
    }

    const facts = picked
        ? [
            `${picked.sessions} session`,
            `${f.tokens(picked.spend.totalTokens)} token`,
            total > 0 ? `${f.percent(picked.spend.costUsd, total)} của ${st.range} ngày` : '',
        ]
        : [
            moved && `${moved} ${st.range} ngày trước đó`,
            worked.length > 0 && `trung bình ${f.usd(total / worked.length)} mỗi ngày có làm việc (${worked.length} ngày)`,
            busiest && busiest.spend.costUsd > 0 && `cao nhất ${f.usd(busiest.spend.costUsd)} vào ${f.dayLabel(busiest.day)}`,
        ];
    const lead = h('section', {class: 'lead hist-lead', 'data-key': 'lead'},
        headline,
        h('p', {class: 'hist-facts'}, facts.filter(Boolean).join(' · ')),
        !prevKnown && !picked && hist.firstDay > days[0].day && h('p', {class: 'hist-note'},
            `Lịch sử bắt đầu từ ${f.dayLabel(hist.firstDay)}/${hist.firstDay.slice(0, 4)}: Claude Code tự xoá transcript cũ hơn, và app chỉ giữ được số liệu từ khi nó đọc được.`),
    );

    const shownSplit: 'project' | 'model' = st.split === 'model' ? 'model' : 'project';
    const whole = picked ? picked.spend.costUsd : total;
    const view = h('div', {class: 'history', 'data-key': 'history', 'data-container': true},
        stale,
        filters,
        h('div', {class: 'overview hist-over', 'data-key': 'over', 'data-container': true}, lead, till(all, hist.today, hist.firstDay, budget)),
        scanNote,
        chart(days, st.split, series, st, hist.today, budget),
        legend(series, st.split),
        h('section', {class: 'ledger hist-ledger', 'data-key': 'ledger', 'data-container': true},
            h('div', {class: 'ledger-cols', 'data-key': 'cols', 'data-container': true},
                strips(picked ? `Theo project, ${f.dayLabel(picked.day)}` : 'Theo project', 'project',
                    rowsOf(days, 'project', st.day), days, series, shownSplit === 'project' && st.split !== 'token', st, whole),
                strips(picked ? `Theo model, ${f.dayLabel(picked.day)}` : 'Theo model', 'model',
                    rowsOf(days, 'model', st.day), days, series, st.split === 'model', st, whole)),
            dayTable(days, st),
            h('p', {class: 'notes hist-foot', 'data-key': 'foot'},
                `Chi phí quy đổi theo giá API (bảng giá ngày ${hist.pricingAsOf}), tính lại theo bảng giá hiện tại cho mọi ngày. ` +
                'Nếu bạn dùng gói subscription, đây không phải số tiền thực trả. App chỉ lưu số token theo ngày, project và model; không lưu nội dung.')),
    );
    return view;
}

// ---- the readout that follows the pointer -------------------------------------------

/** What the tooltip of one day says: every series of that day, values first. */
export function dayReadout(hist: monitor.History, st: HistState, day: string): HTMLElement | null {
    const all = hist.days ?? [];
    const d = all.find(x => x.day === day);
    if (!d) return null;
    const series = seriesOf(all, st.split);
    const values = valuesOf(d, st.split, series);
    const rows = h('ul', {class: 'tip-rows'});
    series.forEach((s, i) => {
        if (values[i] <= 0) return;
        rows.append(h('li', null,
            h('span', {class: `tip-key seg-${s.slot}`, 'aria-hidden': 'true'}),
            h('span', {class: 'tip-value'}, f.usd(values[i])),
            h('span', {class: 'tip-name'}, s.name)));
    });
    return h('div', {class: 'tip-body'},
        h('p', {class: 'tip-day'}, f.dayLong(d.day)),
        h('p', {class: 'tip-total'}, f.usd(d.spend.costUsd),
            d.spend.unpricedTokens > 0 && h('span', {class: 'unpriced'}, '+?')),
        d.spend.totalTokens > 0
            ? h('p', {class: 'tip-meta'}, `${d.sessions} session · ${f.tokens(d.spend.totalTokens)} token`)
            : h('p', {class: 'tip-meta'}, 'Không có session nào chạy'),
        rows.childElementCount > 0 && rows);
}

/** The last days as a strip of bars for the board's till. */
export function trend(hist: monitor.History | null, days: number): HTMLElement | null {
    const all = (hist?.days ?? []).slice(-days);
    if (!hist || all.length === 0 || costOf(all) <= 0) return null;
    const max = Math.max(...all.map(d => d.spend.costUsd));
    const bars = h('span', {class: 'trend-bars', 'aria-hidden': 'true'});
    for (const d of all) {
        const bar = h('i', {class: (d.spend.costUsd > 0 ? 'on' : '') + (d.day === hist.today ? ' is-today' : ''), title: `${f.dayLabel(d.day)}: ${f.usd(d.spend.costUsd)}`});
        if (d.spend.costUsd > 0) bar.style.height = `${Math.max(10, Math.round((d.spend.costUsd / max) * 100))}%`;
        bars.append(bar);
    }
    return h('button', {
        type: 'button', class: 'money-trend', 'data-view': 'history', 'data-fkey': 'trend',
        title: `Chi phí mỗi ngày trong ${all.length} ngày qua. Bấm để mở lịch sử.`,
        'aria-label': `${all.length} ngày qua: ${f.usd(costOf(all))}. Mở lịch sử chi phí.`,
    },
        bars,
        h('span', {class: 'trend-foot'},
            h('span', {class: 'trend-label'}, `${all.length} ngày qua`),
            h('span', {class: 'trend-sum'}, f.usd(costOf(all)))));
}
