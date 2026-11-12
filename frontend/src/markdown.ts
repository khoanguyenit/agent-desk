import {h} from './render';

// Markdown, as far as an agent's answers use it: headings, lists, quotes,
// tables, rules, fenced code, and within a line code, bold, italic, struck
// text and links.
//
// The text comes from a model and is not trusted. Every node is made with
// createElement and filled with text; nothing is ever parsed as HTML, which the
// page's policy forbids in any case. A link is drawn as its words with the
// address in a tooltip, and cannot be followed: the page opens no address.
//
// Whatever is not recognised stays as the text it was. An answer is drawn
// again each time more of it arrives, so a block that has not been closed
// yet, a code fence above all, is drawn as if it ran to the end.

// Spans inside a line are looked for within this many characters. It bounds
// the work a line made of nothing but unclosed marks could cause.
const SPAN = 600;

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

export function inline(text: string, depth = 0): Node[] {
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

const FENCE = /^ {0,3}(`{3,}|~{3,})\s*([^`\s]*)[^`]*$/;
const HEADING = /^ {0,3}(#{1,6})\s+(.*?)(?:\s+#+)?\s*$/;
const RULE = /^ {0,3}([-*_])(?:\s*\1){2,}\s*$/;
const QUOTE = /^ {0,3}>\s?(.*)$/;
const ITEM = /^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/;
const TABLE_RULE = /^\s*\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)*\|?\s*$/;

function indentOf(line: string): number {
    let n = 0;
    for (const c of line) {
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
    return FENCE.test(line) || HEADING.test(line) || RULE.test(line) || QUOTE.test(line) || ITEM.test(line) ||
        (line.includes('|') && next !== undefined && TABLE_RULE.test(next) && next.includes('-'));
}

// How deep lists and quotes may nest before the rest is shown as plain text.
const MAX_NEST = 6;

/** Turns Markdown text into nodes. */
export function markdown(text: string, nest = 0): Node[] {
    const lines = text.replace(/\r\n?/g, '\n').split('\n');
    const out: Node[] = [];
    let i = 0;

    while (i < lines.length) {
        const line = lines[i];
        if (line.trim() === '') {
            i++;
            continue;
        }

        const fence = FENCE.exec(line);
        if (fence) {
            const mark = fence[1];
            const closing = new RegExp(`^ {0,3}${mark[0]}{${mark.length},}\\s*$`);
            const body: string[] = [];
            i++;
            while (i < lines.length && !closing.test(lines[i])) body.push(lines[i++]);
            i++;
            const lang = fence[2].slice(0, 24);
            out.push(h('div', {class: 'md-pre'},
                lang && h('span', {class: 'md-lang'}, lang),
                h('pre', {class: 'msg-code'}, body.join('\n'))));
            continue;
        }

        const heading = HEADING.exec(line);
        if (heading) {
            // The page's own headings stop at the third level; an answer's
            // start below them.
            const level = Math.min(heading[1].length, 3);
            out.push(h(level === 1 ? 'h4' : level === 2 ? 'h5' : 'h6', {class: `md-h md-h${level}`}, ...inline(heading[2])));
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
            while (i < lines.length && QUOTE.test(lines[i])) body.push(QUOTE.exec(lines[i++])![1]);
            out.push(h('blockquote', {class: 'md-quote'},
                ...(nest < MAX_NEST ? markdown(body.join('\n'), nest + 1) : [document.createTextNode(body.join('\n'))])));
            continue;
        }

        if (line.includes('|') && i + 1 < lines.length && TABLE_RULE.test(lines[i + 1]) && lines[i + 1].includes('-')) {
            const head = cells(line);
            const align = cells(lines[i + 1]).map(c => (c.startsWith(':') && c.endsWith(':') ? 'center' : c.endsWith(':') ? 'right' : ''));
            if (head.length > 0 && align.length === head.length) {
                const rows: string[][] = [];
                i += 2;
                while (i < lines.length && lines[i].trim() !== '' && lines[i].includes('|')) rows.push(cells(lines[i++]));
                out.push(tableNode(head, align, rows));
                continue;
            }
        }

        const item = ITEM.exec(line);
        if (item) {
            const base = indentOf(line);
            const ordered = /\d/.test(item[2]);
            const list = h(ordered ? 'ol' : 'ul', {class: 'md-list'});
            if (ordered) {
                const first = parseInt(item[2], 10);
                if (first !== 1) list.setAttribute('start', String(first));
            }
            while (i < lines.length) {
                const m = ITEM.exec(lines[i]);
                // An item of the other kind, or one set further out, begins
                // another list.
                if (!m || indentOf(lines[i]) !== base || /\d/.test(m[2]) !== ordered) break;
                const rest: string[] = [];
                i++;
                while (i < lines.length) {
                    const l = lines[i];
                    if (l.trim() === '') {
                        // A blank line stays in the item only when what
                        // follows is still set under it.
                        const after = lines.slice(i + 1).find(x => x.trim() !== '');
                        if (after === undefined || indentOf(after) <= base) break;
                        rest.push('');
                        i++;
                        continue;
                    }
                    if (indentOf(l) <= base) break;
                    rest.push(l);
                    i++;
                }
                let words = m[3];
                const li = h('li');
                const box = /^\[([ xX])\]\s+(.*)$/.exec(words);
                if (box) {
                    li.className = 'md-task';
                    li.append(h('span', {class: 'md-box', 'aria-hidden': 'true'}, box[1] === ' ' ? '☐' : '☑'),
                        h('span', {class: 'vh'}, box[1] === ' ' ? 'chưa xong: ' : 'đã xong: '));
                    words = box[2];
                }
                // Lines that only carry on the sentence join it; anything
                // else under the item is drawn as blocks of its own.
                const cut = Math.min(...rest.filter(l => l.trim() !== '').map(indentOf));
                const under = rest.map(l => l.slice(Math.min(cut, l.length - l.trimStart().length)));
                let k = 0;
                while (k < under.length && under[k].trim() !== '' && !startsBlock(under[k], under[k + 1])) words += '\n' + under[k++];
                li.append(...inline(words));
                const below = under.slice(k).join('\n');
                if (below.trim() !== '') {
                    li.append(...(nest < MAX_NEST ? markdown(below, nest + 1) : [document.createTextNode('\n' + below)]));
                }
                list.append(li);
                // Blank lines between two items of the same list.
                let j = i;
                while (j < lines.length && lines[j].trim() === '') j++;
                if (j > i && j < lines.length && ITEM.test(lines[j]) && indentOf(lines[j]) === base) i = j;
            }
            out.push(list);
            continue;
        }

        const para: string[] = [line];
        i++;
        while (i < lines.length && lines[i].trim() !== '' && !startsBlock(lines[i], lines[i + 1])) para.push(lines[i++]);
        out.push(h('p', {class: 'msg-prose'}, ...inline(para.join('\n'))));
    }
    return out;
}
