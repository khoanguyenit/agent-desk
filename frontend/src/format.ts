// Formatting for the numbers and times shown on the page. Token figures follow
// the style Claude Code itself uses ("105.3k"), so the two can be compared.

export function tokens(n: number): string {
    if (!Number.isFinite(n) || n <= 0) return '0';
    if (n < 1_000) return String(Math.round(n));
    if (n < 999_950) return (n / 1_000).toFixed(1) + 'k';
    if (n < 9_995_000) return (n / 1_000_000).toFixed(2) + 'M';
    if (n < 99_950_000) return (n / 1_000_000).toFixed(1) + 'M';
    if (n < 999_500_000) return Math.round(n / 1_000_000) + 'M';
    return (n / 1_000_000_000).toFixed(2) + 'B';
}

export function tokensExact(n: number): string {
    return Math.round(n).toLocaleString('en-US');
}

export function usd(n: number): string {
    if (!Number.isFinite(n) || n <= 0) return '$0.00';
    if (n < 0.005) return '<$0.01';
    if (n < 1_000) return '$' + n.toFixed(2);
    return '$' + Math.round(n).toLocaleString('en-US');
}

export function percent(part: number, whole: number): string {
    if (whole <= 0 || part <= 0) return '0%';
    const p = (part / whole) * 100;
    if (p < 0.1) return '<0.1%';
    if (p < 10) return p.toFixed(1) + '%';
    return Math.round(p) + '%';
}

export function duration(ms: number): string {
    const s = Math.max(0, Math.floor(ms / 1000));
    if (s < 60) return `${s}s`;
    const m = Math.floor(s / 60);
    if (m < 60) return `${m}m ${s % 60}s`;
    const hours = Math.floor(m / 60);
    if (hours < 48) return `${hours}h ${m % 60}m`;
    return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

// minutes is for "silent for N minutes" labels.
export function minutes(ms: number): string {
    return `${Math.max(1, Math.round(ms / 60_000))} phút`;
}

export function ago(atMs: number, nowMs: number): string {
    if (!atMs) return '';
    const s = Math.max(0, Math.floor((nowMs - atMs) / 1000));
    // Coarse on purpose below a minute: a label that changed every refresh
    // would force the row to be redrawn every refresh.
    if (s < 60) return 'vài giây trước';
    const m = Math.floor(s / 60);
    if (m < 60) return `${m} phút trước`;
    const hours = Math.floor(m / 60);
    if (hours < 24) return `${hours} giờ trước`;
    return `${Math.floor(hours / 24)} ngày trước`;
}

function two(n: number): string {
    return String(n).padStart(2, '0');
}

export function clock(ms: number): string {
    const d = new Date(ms);
    return `${two(d.getHours())}:${two(d.getMinutes())}:${two(d.getSeconds())}`;
}

export function hourMinute(ms: number): string {
    const d = new Date(ms);
    return `${two(d.getHours())}:${two(d.getMinutes())}`;
}

// ---- usage limits --------------------------------------------------------------

// limitName words a usage limit by Claude Code's name for it, as the
// notifications do (LimitName in internal/monitor/events.go).
export function limitName(kind: string): string {
    if (!kind) return 'giới hạn sử dụng';
    if (kind.startsWith('five_hour')) return 'giới hạn 5 giờ';
    if (kind.startsWith('seven_day')) return 'giới hạn tuần';
    return `giới hạn ${kind}`;
}

// Weekday names as they read in the middle of a sentence.
const WEEKDAYS_INLINE = ['Chủ nhật', 'thứ hai', 'thứ ba', 'thứ tư', 'thứ năm', 'thứ sáu', 'thứ bảy'];

// when words a moment near now: "17:00 hôm nay", "02:00 ngày mai", or
// "02:00 thứ sáu 13/11" further off (WhenText in internal/monitor/events.go).
export function when(atMs: number, nowMs: number): string {
    const t = new Date(atMs);
    const n = new Date(nowMs);
    const sameDay = (a: Date, b: Date) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
    const tomorrow = new Date(n.getFullYear(), n.getMonth(), n.getDate() + 1);
    const clockText = hourMinute(atMs);
    if (sameDay(t, n)) return `${clockText} hôm nay`;
    if (sameDay(t, tomorrow)) return `${clockText} ngày mai`;
    return `${clockText} ${WEEKDAYS_INLINE[t.getDay()]} ${t.getDate()}/${t.getMonth() + 1}`;
}

// limitUntil says until when a limit holds: "tới 02:00 ngày mai", or that
// Claude Code did not say.
export function limitUntil(resetsAtMs: number, nowMs: number): string {
    return resetsAtMs > 0 ? `tới ${when(resetsAtMs, nowMs)}` : 'chưa rõ tới khi nào';
}

// ---- calendar days -------------------------------------------------------------

// A day travels as "2026-11-12", the local date the Go side filed it under.
// It is taken apart by hand: handing the text to Date would read it as UTC.
function dayParts(day: string): [number, number, number] {
    return [Number(day.slice(0, 4)), Number(day.slice(5, 7)), Number(day.slice(8, 10))];
}

export function dayDate(day: string): Date {
    const [y, m, d] = dayParts(day);
    return new Date(y, m - 1, d);
}

const WEEKDAYS = ['CN', 'T2', 'T3', 'T4', 'T5', 'T6', 'T7'];
const WEEKDAYS_LONG = ['Chủ nhật', 'Thứ hai', 'Thứ ba', 'Thứ tư', 'Thứ năm', 'Thứ sáu', 'Thứ bảy'];

/** 0 for Sunday to 6 for Saturday. */
export function weekday(day: string): number {
    return dayDate(day).getDay();
}

/** "04/11" */
export function dayShort(day: string): string {
    return `${day.slice(8, 10)}/${day.slice(5, 7)}`;
}

/** "T3 04/11" */
export function dayLabel(day: string): string {
    return `${WEEKDAYS[weekday(day)]} ${dayShort(day)}`;
}

/** "Thứ ba, 04/11/2026" */
export function dayLong(day: string): string {
    return `${WEEKDAYS_LONG[weekday(day)]}, ${dayShort(day)}/${day.slice(0, 4)}`;
}

/** "tháng 11" */
export function monthLabel(day: string): string {
    return `tháng ${Number(day.slice(5, 7))}`;
}

// usdAxis is for the ticks of a cost axis: whole dollars where they are whole,
// short above a thousand, so every tick of one axis reads in the same style.
export function usdAxis(n: number): string {
    if (n <= 0) return '$0';
    if (n >= 10_000) return '$' + Math.round(n / 1_000) + 'k';
    if (n >= 1_000) return '$' + (n / 1_000).toFixed(1).replace(/\.0$/, '') + 'k';
    if (n >= 10 || Number.isInteger(n)) return '$' + Math.round(n);
    return '$' + n.toFixed(n < 1 ? 2 : 1);
}

// change says how a figure moved against the one it is compared with, as a
// whole percentage; empty when there is nothing to compare with.
export function change(now: number, before: number): string {
    if (before <= 0) return '';
    const p = Math.round(((now - before) / before) * 100);
    if (p === 0) return 'ngang với';
    return p > 0 ? `nhiều hơn ${p}% so với` : `ít hơn ${-p}% so với`;
}
