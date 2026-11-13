// Turns one recording into the page's media: a still picture for each moment
// the demo marked, and two clips, one for watching and one for working.
//
//   node post.mjs <frames dir> <clip.exe> <out dir> <scale>
import fs from 'node:fs';
import path from 'node:path';
import {spawnSync} from 'node:child_process';

const [frames, clip, out, scale] = process.argv.slice(2);
const track = JSON.parse(fs.readFileSync(path.join(frames, 'track.json'), 'utf8'));
fs.rmSync(out, {recursive: true, force: true});
fs.mkdirSync(path.join(out, 'shots'), {recursive: true});

// The recorder keeps a frame only when the window changed, so the picture at
// a moment is the last frame kept before it.
const kept = fs.readdirSync(frames)
    .map(f => /^f-(\d+)\.png$/.exec(f))
    .filter(Boolean)
    .map(m => ({t: Number(m[1]), file: path.join(frames, m[0])}))
    .sort((a, b) => a.t - b.t);
for (const s of track.shots) {
    const before = kept.filter(k => k.t <= s.t);
    const pick = before[before.length - 1] ?? kept[0];
    fs.copyFileSync(pick.file, path.join(out, 'shots', `${s.name}.png`));
    console.log(`shot ${s.name}: frame ${pick.t - s.t} ms from the mark`);
}

// One recording, two clips: everything before the split, and everything
// after it. The pointer's track is the same; what is cut differs.
const far = 9e15;
const parts = [
    {name: 'watch', cut: [[track.split, far]]},
    {name: 'work', cut: [[0, track.split]]},
];
for (const p of parts) {
    const file = path.join(out, `track-${p.name}.json`);
    fs.writeFileSync(file, JSON.stringify({...track, cut: [...(track.cut ?? []), ...p.cut]}));
    const r = spawnSync(clip, ['gif', '-dir', frames, '-track', file, '-out', path.join(out, `${p.name}.gif`), '-fps', '10', '-scale', scale], {stdio: 'inherit'});
    if (r.status !== 0) process.exit(r.status ?? 1);
    console.log(`${p.name}.gif: ${(fs.statSync(path.join(out, `${p.name}.gif`)).size / 1024).toFixed(0)} KB`);
}
