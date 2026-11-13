import {monitor} from '../wailsjs/go/models';
import * as f from './format';
import {h} from './render';

// The summary card: a session's whole work added up, at the top of the side
// panel. The figures come from the Go side (internal/parse/summary.go), which
// reads the whole transcript once and holds no conversation text; this file
// only words them.

const ALLOWED: Record<string, string> = {
    you: 'Bạn duyệt',
    classifier: 'Classifier của chế độ auto',
    mode: 'Chế độ quyền cho phép',
    rule: 'Luật allow',
    hook: 'Hook',
    other: 'Cách khác',
};

const DENIED: Record<string, string> = {
    'user-rejected': 'Bạn từ chối',
    'permission-rule': 'Luật chặn',
    'automode-blocked': 'Chế độ auto chặn',
    'automode-unavailable': 'Chế độ auto không dùng được',
    interrupted: 'Bị ngắt',
    cancelled: 'Bị huỷ',
    rejected: 'Bị từ chối',
};

const MODES: Record<string, string> = {
    auto: 'auto',
    acceptEdits: 'tự sửa file',
    default: 'hỏi trước',
    plan: 'lập kế hoạch',
    bypassPermissions: 'không hỏi gì',
};

function named(items: monitor.Count[] | undefined, names: Record<string, string>): string {
    return (items ?? []).map(c => `${names[c.name] ?? c.name} ${c.n.toLocaleString('vi-VN')}`).join(' · ');
}

function n(v: number): string {
    return v.toLocaleString('vi-VN');
}

/** The one line that stands for the card while it is folded. */
export function summaryLine(s: monitor.Summary): string {
    return [
        `${n(s.prompts)} câu bạn gõ`,
        `${n(s.toolCalls)} lần gọi tool`,
        s.filesChanged > 0 && `${n(s.filesChanged)} file`,
        (s.linesAdded > 0 || s.linesRemoved > 0) && `+${n(s.linesAdded)} −${n(s.linesRemoved)}`,
        s.commits > 0 && `${n(s.commits)} commit`,
        s.pushes > 0 && `${n(s.pushes)} push`,
    ].filter(Boolean).join(' · ');
}

function row(label: string, value: string | Node, title?: string): HTMLElement {
    return h('div', {class: 'sum-row', title}, h('dt', null, label), h('dd', null, value));
}

function group(title: string, ...rows: (HTMLElement | false | '' | null | undefined)[]): HTMLElement {
    return h('section', {class: 'sum-group'}, h('h3', {class: 'sum-title'}, title), h('dl', null, ...rows.filter(Boolean) as HTMLElement[]));
}

/**
 * The card's body. agents are the session's subagents as the board lists
 * them, to split their cost by type; null for a session that has closed,
 * whose agents the board no longer lists.
 */
export function summaryBody(s: monitor.Summary, agents: monitor.Agent[] | null): HTMLElement[] {
    const span = s.firstAtMs > 0 && s.lastAtMs > s.firstAtMs ? s.lastAtMs - s.firstAtMs : 0;
    const files = s.hidden ? [] : s.files ?? [];
    const thinking = s.outputTokens > 0 ? f.percent(s.thinkingTokens, s.outputTokens) : '';

    // What each type of agent cost, most first. Agents of a workflow share
    // one type; the workflow head on the board splits them further.
    const byType = new Map<string, { cost: number; count: number }>();
    for (const a of agents ?? []) {
        const k = a.type || 'agent';
        const cur = byType.get(k) ?? {cost: 0, count: 0};
        cur.cost += a.all.costUsd;
        cur.count++;
        byType.set(k, cur);
    }
    const types = [...byType.entries()].sort((a, b) => b[1].cost - a[1].cost).slice(0, 5);

    const out = [
        group('Đã làm',
            span > 0 && row('Thời gian', `${f.duration(span)} (${f.hourMinute(s.firstAtMs)} – ${f.hourMinute(s.lastAtMs)})`,
                'Từ dòng đầu tới dòng cuối của transcript, gồm cả lúc session nghỉ.'),
            row('Câu bạn gõ', n(s.prompts),
                'Không tính thông báo từ việc chạy nền, tóm tắt sau compact và kết quả lệnh do Claude Code tự đưa vào.'),
            row('Gọi tool', `${n(s.toolCalls)} lần` + ((s.tools ?? []).length > 0 ? `: ${named(s.tools, {})}` : '')),
            s.toolMs > 0 && row('Chạy tool', f.duration(s.toolMs),
                'Cộng thời gian của từng lần gọi tool; các lần chạy song song đều được tính đủ.'),
            s.agents > 0 && row('Agent', n(s.agents) + (s.workflows > 0 ? ` (${n(s.workflows)} workflow)` : '')),
        ),
        group('Thay đổi',
            row('File đã sửa', n(s.filesChanged), 'Các file được sửa hay ghi bằng Edit, Write, NotebookEdit. File đổi qua lệnh shell không nằm ở đây.'),
            files.length > 0 && row('', h('ul', {class: 'sum-files'},
                ...files.map(c => h('li', null, h('span', {class: 'sum-file'}, c.name), c.n > 1 ? h('span', {class: 'sum-times'}, `×${c.n}`) : null)),
                s.filesChanged > files.length ? h('li', {class: 'sum-more'}, `và ${n(s.filesChanged - files.length)} file khác`) : null)),
            row('Dòng', `+${n(s.linesAdded)} −${n(s.linesRemoved)}`, 'Theo bản vá Claude Code ghi kèm mỗi lần sửa file.'),
            row('Git', `${n(s.commits)} commit · ${n(s.pushes)} push`, 'Lệnh git commit và git push đã chạy mà không báo lỗi.'),
        ),
        group('Ai cho phép',
            (s.allowed ?? []).length > 0 && row('Được chạy', named(s.allowed, ALLOWED), 'Ai đã cho từng lần gọi tool chạy, theo Claude Code ghi lại.'),
            (s.denied ?? []).length > 0 && row('Bị từ chối', named(s.denied, DENIED)),
            (s.modes ?? []).length > 0 && row('Chế độ quyền', named(s.modes, MODES), 'Số dòng của bạn được ghi trong mỗi chế độ.'),
        ),
        group('Vì sao tốn',
            row('Lượt gọi model', n(s.responses)),
            thinking !== '' && row('Token suy nghĩ', `${thinking} token output`, 'Phần token output model dùng để suy nghĩ trước khi trả lời. Tính tiền như output.'),
            (s.efforts ?? []).length > 0 && row('Mức effort', named(s.efforts, {})),
            row('Compact', s.compactions > 0
                ? `${n(s.compactions)} lần, lần cuối ${f.tokens(s.compactedFrom)} → ${f.tokens(s.compactedTo)} token`
                : 'Chưa lần nào', 'Context được tóm tắt lại để còn chỗ. Sau đó model làm việc với bản tóm tắt.'),
            row('Cache miss', s.cacheMisses > 0
                ? `${n(s.cacheMisses)} lần` + (s.cacheMissTokens > 0 ? `, gửi lại ${f.tokens(s.cacheMissTokens)} token` : '')
                : 'Không', 'Lần gọi không dùng lại được context đã cache, thường sau khi nghỉ lâu hay đổi model: phần context đó phải gửi và trả tiền lại.'),
            types.length > 0 && row('Theo loại agent', types.map(([t, v]) => `${t} ${f.usd(v.cost)} (${v.count})`).join(' · '),
                'Chi phí của các agent đang có trên bảng, cộng theo loại agent. Chi phí quy theo giá API.'),
        ),
    ];
    if (s.hidden) out.push(h('p', {class: 'sum-note'}, 'Tên file đang ẩn theo công tắc "Ẩn nội dung".'));
    if (s.partial) out.push(h('p', {class: 'sum-note'}, 'Có transcript không đọc trọn được, nên số liệu có thể thiếu.'));
    return out;
}
