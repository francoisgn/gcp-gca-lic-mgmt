// Generates the isometric hero illustration (laptop + cup) as a standalone SVG.
const C = Math.cos(Math.PI / 6), S = Math.sin(Math.PI / 6);
let minX = 1e9, minY = 1e9, maxX = -1e9, maxY = -1e9;
const iso = (x, y, z = 0) => {
  const X = (x - y) * C, Y = (x + y) * S - z;
  minX = Math.min(minX, X); maxX = Math.max(maxX, X); minY = Math.min(minY, Y); maxY = Math.max(maxY, Y);
  return [X, Y];
};
const f = n => Math.round(n * 10) / 10;
const pts = arr => arr.map(p => `${f(p[0])},${f(p[1])}`).join(' ');
const poly = (arr, attrs) => `<polygon points="${pts(arr)}" ${attrs}/>`;
const rhombus = (x0, y0, x1, y1, z) => [iso(x0, y0, z), iso(x1, y0, z), iso(x1, y1, z), iso(x0, y1, z)];
const out = [];

// Ground outlines (platform under the laptop, floating diamonds).
out.push(poly(rhombus(-30, -30, 250, 185, -10), 'fill="none" stroke="url(#outline)" stroke-width="2.2" stroke-linejoin="round"'));
for (const [x, y, s] of [[290, 40, 46], [300, 150, 26], [-200, 210, 40], [-120, 300, 22], [150, 270, 30]]) {
  out.push(poly(rhombus(x, y, x + s, y + s, -10), 'fill="none" stroke="url(#outline)" stroke-width="2" stroke-linejoin="round" opacity=".9"'));
}

// Laptop base.
const W = 220, D = 150, H = 9;
out.push(poly([iso(W, 0, H), iso(W, D, H), iso(W, D, 0), iso(W, 0, 0)], 'fill="#8d7cc0"'));
out.push(poly([iso(0, D, H), iso(W, D, H), iso(W, D, 0), iso(0, D, 0)], 'fill="#a993d6"'));
out.push(poly(rhombus(0, 0, W, D, H), 'fill="url(#body)"'));
// Keyboard.
const cols = 14, rows = 5, kx0 = 22, kx1 = 198, ky0 = 18, ky1 = 82, gap = 3;
const kw = (kx1 - kx0) / cols, kh = (ky1 - ky0) / rows;
out.push(poly(rhombus(kx0 - 4, ky0 - 4, kx1 + 4, ky1 + 4, H), 'fill="#9a86c9" opacity=".55"'));
for (let r = 0; r < rows; r++) for (let c = 0; c < cols; c++) {
  let x0 = kx0 + c * kw, x1 = x0 + kw - gap;
  if (r === rows - 1 && c > 3 && c < 10) { if (c !== 4) continue; x1 = kx0 + 10 * kw - gap; } // space bar
  const y0 = ky0 + r * kh, y1 = y0 + kh - gap;
  out.push(poly(rhombus(x0, y0, x1, y1, H + 0.6), 'fill="#45475a"'));
}
// Trackpad.
out.push(poly(rhombus(78, 98, 142, 136, H + 0.2), 'fill="#d9c7f5" stroke="#9a86c9" stroke-width="1.2"'));

// Screen (hinge at y=0, tilted backwards).
const sx0 = 8, sx1 = 212, tilt = 34, SH = 150;
const P = (x, v) => iso(x, -tilt * v, H + SH * v);
out.push(poly([P(sx0, 0), P(sx1, 0), P(sx1, 1), P(sx0, 1)], 'fill="url(#lid)"'));
const dx0 = 18, dx1 = 202, dv0 = 0.07, dv1 = 0.93;
out.push(poly([P(dx0, dv0), P(dx1, dv0), P(dx1, dv1), P(dx0, dv1)], 'fill="#181825"'));
out.push(poly([P(dx0 + 10, dv0 + 0.05), P(dx1 - 10, dv0 + 0.05), P(dx1 - 10, dv1 - 0.04), P(dx0 + 10, dv1 - 0.04)], 'fill="#1e1e2e"'));
// Code lines.
const palette = ['#f38ba8', '#fab387', '#a6e3a1', '#89b4fa', '#cba6f7', '#94e2d5', '#f9e2af', '#cdd6f4'];
const lines = [[0, 22, 0], [0, 14, 1], [1, 30, 2], [1, 18, 3], [2, 40, 4], [2, 26, 5], [1, 12, 6], [0, 34, 7], [1, 22, 2], [2, 16, 3], [2, 44, 1], [1, 20, 4]];
let v = 0.82;
for (const [indent, len, col] of lines) {
  let x = dx0 + 22 + indent * 12;
  const seg = [[len, palette[col]], [len * 0.6, palette[(col + 3) % palette.length]]];
  for (const [l, color] of seg) {
    const a = P(x, v), b = P(x + l, v);
    out.push(`<line x1="${f(a[0])}" y1="${f(a[1])}" x2="${f(b[0])}" y2="${f(b[1])}" stroke="${color}" stroke-width="4.2" stroke-linecap="round"/>`);
    x += l + 8;
  }
  v -= 0.058;
}
// Gemini sparkle on screen corner.
{
  const c = P(dx1 - 26, 0.86);
  out.push(`<path transform="translate(${f(c[0] - 9)},${f(c[1] - 9)}) scale(.75)" d="M12 0C12 6.6 17.4 12 24 12C17.4 12 12 17.4 12 24C12 17.4 6.6 12 0 12C6.6 12 12 6.6 12 0Z" fill="url(#gemini)"/>`);
}

// Coffee cup (iso cylinder) on its coaster.
const cx = -95, cy = 70, r = 24, ch = 68;
out.push(poly(rhombus(cx - 42, cy - 42, cx + 42, cy + 42, -10), 'fill="none" stroke="url(#outline)" stroke-width="2.2" stroke-linejoin="round"'));
const rx = r * Math.SQRT2 * C, ry = r * Math.SQRT2 * S;
const [bx, by] = iso(cx, cy, 0), [tx, ty] = iso(cx, cy, ch);
out.push(`<path d="M${f(bx - rx)},${f(ty)} L${f(bx - rx)},${f(by)} A${f(rx)},${f(ry)} 0 0 0 ${f(bx + rx)},${f(by)} L${f(bx + rx)},${f(ty)} Z" fill="url(#cup)"/>`);
// Lid.
const lh = 10, lr = 1.08;
const [lx, ly] = iso(cx, cy, ch + lh);
out.push(`<path d="M${f(tx - rx * lr)},${f(ly)} L${f(tx - rx * lr)},${f(ty)} A${f(rx * lr)},${f(ry * lr)} 0 0 0 ${f(tx + rx * lr)},${f(ty)} L${f(tx + rx * lr)},${f(ly)} Z" fill="#b4befe"/>`);
out.push(`<ellipse cx="${f(lx)}" cy="${f(ly)}" rx="${f(rx * lr)}" ry="${f(ry * lr)}" fill="#cdd6f4"/>`);
out.push(`<ellipse cx="${f(lx)}" cy="${f(ly)}" rx="${f(rx * 0.72)}" ry="${f(ry * 0.72)}" fill="#bac2de"/>`);
out.push(`<ellipse cx="${f(lx + rx * 0.35)}" cy="${f(ly + ry * 0.35)}" rx="5" ry="2.6" fill="#7f849c"/>`);
// Paw print.
const [px, py] = iso(cx, cy, ch * 0.42);
out.push(`<g fill="#11111b" transform="translate(${f(px + 2)},${f(py + 8)})"><ellipse cx="0" cy="4" rx="7" ry="5.6"/><circle cx="-7.5" cy="-4" r="2.6"/><circle cx="-2.5" cy="-8" r="2.6"/><circle cx="2.8" cy="-8" r="2.6"/><circle cx="7.8" cy="-4" r="2.6"/></g>`);
// Steam.
out.push(`<path d="M${f(lx - 4)},${f(ly - 14)} c-14,-18 12,-30 -2,-52 c-10,-16 6,-28 2,-40" fill="none" stroke="url(#steam)" stroke-width="5" stroke-linecap="round"/>`);
out.push(`<path d="M${f(lx + 10)},${f(ly - 18)} c-8,-12 8,-22 0,-36" fill="none" stroke="url(#steam)" stroke-width="3.5" stroke-linecap="round" opacity=".7"/>`);

const pad = 24;
const vb = [f(minX - pad), f(minY - pad - 60), f(maxX - minX + 2 * pad), f(maxY - minY + 2 * pad + 60)];
const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${vb.join(' ')}" role="img" aria-label="Isometric laptop with a cup of coffee">
<!-- Generated by scripts/gen-hero.mjs — isometric illustration inspired by the Catppuccin visual identity. -->
<defs>
<linearGradient id="outline" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#fab387"/><stop offset=".55" stop-color="#f5c2e7"/><stop offset="1" stop-color="#cba6f7"/></linearGradient>
<linearGradient id="body" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#f5c2e7"/><stop offset=".5" stop-color="#cba6f7"/><stop offset="1" stop-color="#b4befe"/></linearGradient>
<linearGradient id="lid" x1="0" y1="1" x2="1" y2="0"><stop offset="0" stop-color="#cba6f7"/><stop offset="1" stop-color="#b4befe"/></linearGradient>
<linearGradient id="cup" x1="0" y1="0" x2="1" y2="0"><stop offset="0" stop-color="#b4befe"/><stop offset=".6" stop-color="#cba6f7"/><stop offset="1" stop-color="#9a86c9"/></linearGradient>
<linearGradient id="steam" x1="0" y1="1" x2="0" y2="0"><stop offset="0" stop-color="#9399b2" stop-opacity=".9"/><stop offset="1" stop-color="#9399b2" stop-opacity="0"/></linearGradient>
<linearGradient id="gemini" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#4796E4"/><stop offset=".5" stop-color="#847ACE"/><stop offset="1" stop-color="#C3677F"/></linearGradient>
</defs>
${out.join('\n')}
</svg>
`;
process.stdout.write(svg);
