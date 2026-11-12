import {h} from './render';

// Markdown, as far as an agent's answers use it: headings, lists, quotes,
// tables, rules, fenced code, and within a line code, bold, italic, struck
// text and links.
//
// The text comes from a model and is not trusted. Two things follow.
//
// Nothing is ever parsed as HTML: every node is made with createElement and
// filled with text, which is also all the page's policy allows. A link is
// drawn as its words with the address in a tooltip, and cannot be followed:
// the page opens no address.
//
// And the work must stay in proportion to the text, whatever the text is. An
// answer is drawn again each time more of it arrives, and while it is being
// drawn the user cannot answer a request for permission. So every pattern
// here is bounded: a line longer than a line of prose ever is goes in as it
// stands, spans are looked for within a fixed distance, a table has a
// largest size, nesting has a depth, and nothing looks ahead further than
// once per line.
//
// Whatever is not recognised stays as the text it was. A block that has not
// been closed yet, a code fence above all, is drawn as if it ran to the end.

// Spans inside a line are looked for within this many characters.
const SPAN = 600;
// A line longer than this is not looked into at all.
const LONG_LINE = 2000;
// A text longer than this is drawn as plain text. What the app keeps of an
// answer is far shorter; this is the net under that.
const MAX_TEXT = 300_000;
// The largest table that is drawn as one. A row makes a cell for every
// column of the heading, so without a bound a short text could ask for
// millions of cells.
const MAX_COLUMNS = 40;
const MAX_CELLS = 4000;

const INLINE = new RegExp([
    // 1: a character escaped with a backslash
    '\\\\([\\\\`*_{}\\[\\]()#+\\-.!~|>])',
    // 2, 3: code between runs of backticks of equal length
    '(`+)([^\\n]{1,' + SPAN + '}?)\\2(?!`)',
    // 4, 5: bold
    '\\*\\*([^\\s*](?:[^\\n]{0,' + SPAN + '}?[^\\s*])?)\\*\\*',
    '__([^\\s_](?:[^\\n]{0,' + SPAN + '}?[^\\s_])?)__',
    // 6: struck
    '~~([^\\s~](?:[^\\n]{0,' + SPAN + '}?[^\\s~])?)~~',
    // 7, 8: italic; an underscore inside a word is part of the word
    '\\*([^\\s*](?:[^*\\n]{0,' + SPAN + '}?[^\\s*])?)\\*',
    '(?<![A-Za-z0-9_])_([^\\s_](?:[^_\\n]{0,' + SPAN + '}?[^\\s_])?)_(?![A-Za-z0-9_])',
    // 9, 10: [words](address)
    '\\[([^\\]\\n]{1,' + SPAN + '})\\]\\(([^)\\s]{1,2000})(?:\\s+"[^"\\n]{0,200}")?\\)',
].join('|'), 'g');

// How deep spans may nest ("**bold with *italic* inside**"). Past it the text
// is left as it is.
const MAX_DEPTH = 4;
// A run of text longer than this is not searched for spans: no paragraph a
// person reads is, and the search costs time in proportion to it.
const MAX_RUN = 20_000;

export function inline(text: string, depth = 0): Node[] {
    if (text.length > MAX_RUN) return [document.createTextNode(text)];
    const out: Node[] = [];
    let last = 0;
    const plain = (to: number) => {
        if (to > last) out.push(document.createTextNode(text.slice(last, to)));
    };
    const inner = (s: string): Node[] => (depth < MAX_DEPTH ? inline(s, depth + 1) : [document.createTextNode(s)]);

    INLINE.lastIndex = 0;
    for (const m of text.matchAll(INLINE)) {
        const at = m.index ?? 0;
        plain(at);
        last = at + m[0].length;
        if (m[1] !== undefined) out.push(document.createTextNode(m[1]));
        else if (m[3] !== undefined) out.push(h('code', {class: 'md-code'}, m[3].trim() === '' ? m[3] : m[3].replace(/^ (.*) $/, '$1')));
        else if (m[4] !== undefined || m[5] !== undefined) out.push(h('strong', null, ...inner(m[4] ?? m[5])));
        else if (m[6] !== undefined) out.push(h('s', null, ...inner(m[6])));
        else if (m[7] !== undefined || m[8] !== undefined) out.push(h('em', null, ...inner(m[7] ?? m[8])));
        else if (m[9] !== undefined) {
            // The words are shown; where they point is there to be read, not
            // followed.
            out.push(h('span', {class: 'md-link', title: m[10]}, ...inner(m[9])));
        }
    }
    plain(text.length);
    return out;
}

// The patterns for whole lines. Each is anchored and has no two parts that
// could share the same characters, so none can be made to try a line over and
// over; and no line longer than LONG_LINE reaches them.
const FENCE = /^ {0,3}(`{3,}|~{3,})([^`]*)$/;
const HEADING = /^ {0,3}(#{1,6})(?:[ \t]+(.*))?$/;
const RULE = /^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/;
const QUOTE = /^ {0,3}>[ \t]?(.*)$/;
const ITEM = /^([ \t]*)([-*+]|\d{1,9}[.)])[ \t]+(.*)$/;
const RULE_CELL = /^:?-+:?$/;

function indentOf(line: string): number {
    let n = 0;
    for (let i = 0; i < line.length; i++) {
        const c = line[i];
        if (c === ' ') n++;
        else if (c === '\t') n += 4;
        else break;
    }
    return n;
}

// cells splits a table row at its bars. A bar escaped with a backslash, or
// inside a code span, is part of the cell.
function cells(line: string): string[] {
    const out: string[] = [];
    let cur = '';
    let code = false;
    for (let i = 0; i < line.length; i++) {
        const c = line[i];
        if (c === '\\' && line[i + 1] === '|') {
            cur += '|';
            i++;
        } else if (c === '`') {
            code = !code;
            cur += c;
        } else if (c === '|' && !code) {
            out.push(cur);
            cur = '';
        } else {
            cur += c;
        }
    }
    out.push(cur);
    // The bars at the two ends of a row enclose it; they do not make cells.
    if (out.length > 1 && out[0].trim() === '') out.shift();
    if (out.length > 1 && out[out.length - 1].trim() === '') out.pop();
    return out.map(c => c.trim());
}

// tableRule reports whether a line is the row of dashes under a table's
// heading: cells of dashes, with a colon at an end to say how a column lines up.
function tableRule(line: string): boolean {
    if (line.length > LONG_LINE || !line.includes('-')) return false;
    const parts = cells(line);
    return parts.length > 0 && parts.every(c => RULE_CELL.test(c));
}

function tableNode(head: string[], align: string[], rows: string[][]): HTMLElement {
    const cls = (i: number) => (align[i] ? `md-${align[i]}` : null);
    const thead = h('thead', null, h('tr', null, ...head.map((c, i) => h('th', {scope: 'col', class: cls(i)}, ...inline(c)))));
    const tbody = h('tbody');
    for (const r of rows) {
        tbody.append(h('tr', null, ...head.map((_, i) => h('td', {class: cls(i)}, ...inline(r[i] ?? '')))));
    }
    // A wide table scrolls inside its own box instead of widening the panel.
    return h('div', {class: 'md-table-wrap', tabindex: 0, role: 'group', 'aria-label': 'Bảng'}, h('table', {class: 'md-table'}, thead, tbody));
}

function startsBlock(line: string, next: string | undefined): boolean {
    return line.length > LONG_LINE || FENCE.test(line) || HEADING.test(line) || RULE.test(line) || QUOTE.test(line) || ITEM.test(line) ||
        (line.includes('|') && next !== undefined && tableRule(next));
}

// How deep lists and quotes may nest before the rest is shown as plain text.
const MAX_NEST = 6;

const text = (s: string) => h('p', {class: 'msg-prose'}, s);

/** Turns Markdown text into nodes. */
export function markdown(source: string, nest = 0): Node[] {
    if (source.length > MAX_TEXT) return [text(source)];
    const lines = source.replace(/\r\n?/g, '\n').split('\n');
    const blank = lines.map(l => l.length <= LONG_LINE && l.trim() === '');
    // For each line, the next one after it that is not blank; worked out
    // once, so that looking past blank lines never costs a pass of its own.
    const after: number[] = new Array(lines.length);
    for (let i = lines.length - 1, next = -1; i >= 0; i--) {
        after[i] = next;
        if (!blank[i]) next = i;
    }
    const nested = (body: string): Node[] => (nest < MAX_NEST ? markdown(body, nest + 1) : [document.createTextNode(body)]);

    const out: Node[] = [];
    let i = 0;
    while (i < lines.length) {
        const line = lines[i];
        if (blank[i]) {
            i++;
            continue;
        }
        if (line.length > LONG_LINE) {
            // Not a line anyone wrote as Markdown. It goes in as it stands.
            out.push(text(line));
            i++;
            continue;
        }

        const fence = FENCE.exec(line);
        if (fence) {
            const mark = fence[1];
            const body: string[] = [];
            i++;
            // The fence closes at a line that is nothing but as many of its
            // marks, or more.
            const closes = (l: string) => {
                const t = l.trim();
                return t.length >= mark.length && t.length <= LONG_LINE && indentOf(l) <= 3 && t.split(mark[0]).join('') === '';
            };
            while (i < lines.length && !closes(lines[i])) body.push(lines[i++]);
            i++;
            const lang = fence[2].trim().split(/\s/)[0].slice(0, 24);
            out.push(h('div', {class: 'md-pre'},
                lang ? h('span', {class: 'md-lang'}, lang) : null,
                h('pre', {class: 'msg-code'}, body.join('\n'))));
            continue;
        }

        const heading = HEADING.exec(line);
        if (heading) {
            // A run of hashes after the words closes the heading and is not
            // part of it.
            let words = (heading[2] ?? '').trimEnd();
            let end = words.length;
            while (end > 0 && words[end - 1] === '#') end--;
            if (end < words.length && (end === 0 || words[end - 1] === ' ' || words[end - 1] === '\t')) words = words.slice(0, end).trimEnd();
            // The page's own headings stop at the third level; an answer's
            // start below them.
            const level = Math.min(heading[1].length, 3);
            out.push(h(level === 1 ? 'h4' : level === 2 ? 'h5' : 'h6', {class: `md-h md-h${level}`}, ...inline(words)));
            i++;
            continue;
        }

        if (RULE.test(line)) {
            out.push(h('hr', {class: 'md-rule'}));
            i++;
            continue;
        }

        if (QUOTE.test(line)) {
            const body: string[] = [];
            while (i < lines.length && lines[i].length <= LONG_LINE && QUOTE.test(lines[i])) body.push(QUOTE.exec(lines[i++])![1]);
            out.push(h('blockquote', {class: 'md-quote'}, ...nested(body.join('\n'))));
            continue;
        }

        if (line.includes('|') && i + 1 < lines.length && tableRule(lines[i + 1])) {
            const head = cells(line);
            const align = cells(lines[i + 1]).map(c => (c.startsWith(':') && c.endsWith(':') ? 'center' : c.endsWith(':') ? 'right' : ''));
            if (head.length > 0 && align.length === head.length) {
                const start = i;
                const rows: string[][] = [];
                i += 2;
                while (i < lines.length && !blank[i] && lines[i].length <= LONG_LINE && lines[i].includes('|')) rows.push(cells(lines[i++]));
                if (head.length <= MAX_COLUMNS && head.length * (rows.length + 1) <= MAX_CELLS) {
                    out.push(tableNode(head, align, rows));
                } else {
                    // Too large to be laid out as a table: shown as written.
                    out.push(h('pre', {class: 'msg-code'}, lines.slice(start, i).join('\n')));
                }
                continue;
            }
        }

        const item = ITEM.exec(line);
        if (item) {
            const base = indentOf(line);
            const ordered = item[2].length > 1 || (item[2] >= '0' && item[2] <= '9');
            const list = h(ordered ? 'ol' : 'ul', {class: 'md-list'});
            if (ordered) {
                const first = parseInt(item[2], 10);
                if (first !== 1) list.setAttribute('start', String(first));
            }
            while (i < lines.length) {
                const m = lines[i].length <= LONG_LINE ? ITEM.exec(lines[i]) : null;
                // An item of the other kind, or one set further out, begins
                // another list.
                if (!m || indentOf(lines[i]) !== base || (m[2].length > 1 || (m[2] >= '0' && m[2] <= '9')) !== ordered) break;
                const rest: string[] = [];
                i++;
                while (i < lines.length) {
                    if (blank[i]) {
                        // A blank line stays in the item only when what
                        // follows is still set under it.
                        const next = after[i];
                        if (next < 0 || indentOf(lines[next]) <= base) break;
                        rest.push('');
                        i++;
                        continue;
                    }
                    if (indentOf(lines[i]) <= base) break;
                    rest.push(lines[i]);
                    i++;
                }
                let words = m[3];
                const li = h('li');
                const box = /^\[([ xX])\][ \t]+(.*)$/.exec(words);
                if (box) {
                    li.className = 'md-task';
                    li.append(h('span', {class: 'md-box', 'aria-hidden': 'true'}, box[1] === ' ' ? '☐' : '☑'),
                        h('span', {class: 'vh'}, box[1] === ' ' ? 'chưa xong: ' : 'đã xong: '));
                    words = box[2];
                }
                // What is set under the item is brought out to the margin.
                let cut = Infinity;
                for (const l of rest) {
                    if (l.trim() !== '') cut = Math.min(cut, indentOf(l));
                }
                const under = rest.map(l => l.slice(Math.min(cut, l.length - l.trimStart().length)));
                // Lines that only carry on the sentence join it; anything
                // else under the item is drawn as blocks of its own.
                let k = 0;
                while (k < under.length && under[k].trim() !== '' && !startsBlock(under[k], under[k + 1])) words += '\n' + under[k++];
                li.append(...inline(words));
                const below = under.slice(k).join('\n');
                if (below.trim() !== '') li.append(...nested(below));
                list.append(li);
                // Blank lines between two items of the same list.
                if (i < lines.length && blank[i]) {
                    const next = after[i];
                    if (next >= 0 && lines[next].length <= LONG_LINE && ITEM.test(lines[next]) && indentOf(lines[next]) === base) i = next;
                }
            }
            out.push(list);
            continue;
        }

        const para: string[] = [line];
        i++;
        while (i < lines.length && !blank[i] && !startsBlock(lines[i], lines[i + 1])) para.push(lines[i++]);
        out.push(h('p', {class: 'msg-prose'}, ...inline(para.join('\n'))));
    }
    return out;
}
