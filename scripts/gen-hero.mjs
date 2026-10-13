// Generates the hero illustration of the landing page as a standalone SVG:
// an isometric 1980s desk (CRT monitor on its system unit, keyboard, floppy
// disks, mug) and, on the wall behind, a faded black & white basketball
// poster (stylised drawing, no photo, no team or league marks).
//
//   node scripts/gen-hero.mjs > internal/web/static/img/hero-desk.svg   (make hero)
//
// World axes: x to the right, y towards the viewer, z up. Isometric projection.
const C = Math.cos(Math.PI / 6), S = Math.sin(Math.PI / 6);
let minX = 1e9, minY = 1e9, maxX = -1e9, maxY = -1e9;
const iso = (x, y, z = 0) => {
  const X = (x - y) * C, Y = (x + y) * S - z;
  minX = Math.min(minX, X); maxX = Math.max(maxX, X); minY = Math.min(minY, Y); maxY = Math.max(maxY, Y);
  return [X, Y];
};
const f = (n) => Math.round(n * 10) / 10;
const pts = (arr) => arr.map((p) => `${f(p[0])},${f(p[1])}`).join(' ');
const poly = (arr, attrs) => `<polygon points="${pts(arr)}" ${attrs}/>`;
const top = (x0, y0, x1, y1, z) => [iso(x0, y0, z), iso(x1, y0, z), iso(x1, y1, z), iso(x0, y1, z)];
const out = [];

/** Visible faces of a box: right (+x), front (+y), top. */
function box(x0, y0, z0, x1, y1, z1, { right, front, topFill, extra = '' }) {
  out.push(poly([iso(x1, y0, z1), iso(x1, y1, z1), iso(x1, y1, z0), iso(x1, y0, z0)], `fill="${right}" ${extra}`));
  out.push(poly([iso(x0, y1, z1), iso(x1, y1, z1), iso(x1, y1, z0), iso(x0, y1, z0)], `fill="${front}" ${extra}`));
  out.push(poly(top(x0, y0, x1, y1, z1), `fill="${topFill}" ${extra}`));
}

/**
 * Group drawn flat (u right, v down, 1 unit = 1 world unit) on a vertical
 * plane facing the viewer (+y), anchored at its top-left corner (x, y, z).
 */
function onFront(x, y, z, w, h, body, attrs = '', k = 1) {
  iso(x + w * k, y, z - h * k); // extend the bounds
  const [e, g] = iso(x, y, z);
  const m = (n) => f(n * k * 1000) / 1000;
  return `<g transform="matrix(${m(C)} ${m(S)} 0 ${m(1)} ${f(e)} ${f(g)})" ${attrs}>${body}</g>`;
}

// Palette: Catppuccin Mocha surfaces, faded beige for the 80s hardware and
// the UI accent gradient (teal → green → lime) for the details.
const beige = { top: '#d8d1c2', front: '#c3bba9', right: '#a59d8c', dark: '#8f8778', key: '#ebe5d8' };

// ---------------------------------------------------------------------------
// 1. Wall and poster (behind everything, faded and slightly blurred = depth)
// ---------------------------------------------------------------------------
const wallY = -16;

const PW = 112, PH = 144; // poster size
const poster = `
<rect width="${PW}" height="${PH}" rx="1.5" fill="#d6d6d6"/>
<rect x="6" y="6" width="${PW - 12}" height="${PH - 26}" fill="url(#blocks)"/>
<g clip-path="url(#photo)">
  <ellipse cx="70" cy="40" rx="20" ry="24" fill="#8c8c8c" opacity=".55"/>
  <!-- raised hand -->
  <path d="M15 110 L31 110 L31 66 L18 66 Z" fill="#4a4a4a"/>
  <rect x="13" y="47" width="19" height="21" rx="5" fill="#4a4a4a"/>
  <g fill="#4a4a4a">
    <rect x="13.4" y="29" width="4.2" height="22" rx="2.1" transform="rotate(-10 15.5 50)"/>
    <rect x="18.2" y="25" width="4.4" height="25" rx="2.2" transform="rotate(-4 20.4 50)"/>
    <rect x="23" y="26" width="4.4" height="24" rx="2.2" transform="rotate(3 25.2 50)"/>
    <rect x="27.6" y="31" width="4" height="20" rx="2" transform="rotate(10 29.6 50)"/>
  </g>
  <!-- body and jersey -->
  <path d="M24 125 L27 82 Q30 68 44 64 L84 64 Q97 68 99 82 L102 125 Z" fill="#4a4a4a"/>
  <rect x="58" y="52" width="13" height="14" fill="#444"/>
  <path d="M36 125 L35 86 Q37 72 47 69 L50 69 Q55 84 64.5 84 Q74 84 79 69 L82 69 Q91 72 92 86 L93 125 Z" fill="#efefef"/>
  <path d="M47 69 Q37 72 35 86 M82 69 Q91 72 92 86 M50 69 Q55 84 64.5 84 Q74 84 79 69" fill="none" stroke="#2a2a2a" stroke-width="2"/>
  <text x="64.5" y="112" text-anchor="middle" font-family="Arial Black, Helvetica, Arial, sans-serif" font-weight="900" font-size="21" fill="#5a5a5a" stroke="#2a2a2a" stroke-width=".8">23</text>
  <!-- elbow band and cigars -->
  <rect x="94" y="92" width="8" height="9" fill="#262626"/>
  <path d="M74 121 L98 116 M76 124 L99 119" stroke="#bdbdbd" stroke-width="3" stroke-linecap="round"/>
  <!-- head, cap, smile, cigar -->
  <ellipse cx="64.5" cy="40" rx="12" ry="14.5" fill="#4a4a4a"/>
  <ellipse cx="52.5" cy="42" rx="2.4" ry="3.6" fill="#404040"/>
  <path d="M51.5 37 Q51 22 65 21.5 Q78.5 22 78 36 Q65 31 51.5 37 Z" fill="#1f1f1f"/>
  <path d="M50.5 35 Q64.5 27 79 34.5 Q64.5 31.5 50.5 35 Z" fill="#6a6a6a"/>
  <path d="M57.5 46 Q65 52.5 72 46 Q65 49 57.5 46 Z" fill="#f7f7f7"/>
  <path d="M69 48.5 L85 43" stroke="#cfcfcf" stroke-width="3" stroke-linecap="round"/>
  <circle cx="85.4" cy="42.9" r="1.6" fill="#8a8a8a"/>
  <path d="M86 41 C 83 36, 90 33, 87 28 C 85 25, 89 22, 88 19" fill="none" stroke="#f0f0f0" stroke-width="1.2" opacity=".7"/>
</g>
<rect x="6" y="${PH - 15}" width="44" height="3" rx="1.5" fill="#8a8a8a"/>
<rect x="6" y="${PH - 9.5}" width="26" height="2" rx="1" fill="#a6a6a6"/>
<rect x="${PW / 2 - 9}" y="-4" width="18" height="8" fill="#eeeeee" opacity=".55"/>`;
// Partly hidden by the monitor: the overlap gives the depth.
const posterX = 60, posterZ = 204, posterK = 0.85;
{
  // Soft light on the wall around the poster (no hard wall edges, not in the bounds).
  const [e, g] = [(posterX - wallY) * C, (posterX + wallY) * S - posterZ];
  out.push(`<g transform="matrix(${f(C * 1000) / 1000} ${S} 0 1 ${f(e)} ${f(g)})"><ellipse cx="${PW * posterK / 2}" cy="${PH * posterK / 2}" rx="150" ry="130" fill="url(#wall)"/></g>`);
}
out.push(onFront(posterX, wallY + 0.5, posterZ, PW, PH, poster, 'filter="url(#depth)" opacity=".5"', posterK));

// ---------------------------------------------------------------------------
// 2. Desk
// ---------------------------------------------------------------------------
const DX0 = -30, DX1 = 340, DY0 = 0, DY1 = 300, DT = 12;
for (const [lx, ly] of [[DX1 - 16, DY0 + 4], [DX0 + 4, DY1 - 16], [DX1 - 16, DY1 - 16]]) {
  box(lx, ly, -DT - 36, lx + 12, ly + 12, -DT, { right: 'url(#legR)', front: 'url(#legF)', topFill: '#1e1e2e' });
}
box(DX0, DY0, -DT, DX1, DY1, 0, { right: '#1b1b28', front: '#232334', topFill: 'url(#desk)' });
out.push(poly(top(DX0, DY0, DX1, DY1, 0), 'fill="none" stroke="url(#accent)" stroke-width="1.6" stroke-linejoin="round" opacity=".55"'));
// Screen glow on the desk.
{
  const [gx, gy] = iso(130, 200, 0);
  out.push(`<ellipse cx="${f(gx)}" cy="${f(gy)}" rx="150" ry="62" fill="url(#glow)"/>`);
}

// ---------------------------------------------------------------------------
// 3. Floppy disks (5¼")
// ---------------------------------------------------------------------------
for (const [x, y, z, label] of [[256, 40, 0, '#bfd38a'], [262, 50, 2.4, '#8fcfba']]) {
  box(x, y, z, x + 54, y + 54, z + 2.4, { right: '#11111b', front: '#181825', topFill: '#26263a' });
  out.push(poly(top(x + 8, y + 4, x + 46, y + 18, z + 2.5), `fill="${label}" opacity=".85"`));
  const [hx, hy] = iso(x + 27, y + 32, z + 2.5);
  out.push(`<ellipse cx="${f(hx)}" cy="${f(hy)}" rx="10.4" ry="6" fill="#0b0b12"/>`);
  out.push(`<ellipse cx="${f(hx)}" cy="${f(hy)}" rx="4.2" ry="2.4" fill="#45475a"/>`);
}

// ---------------------------------------------------------------------------
// 4. System unit + CRT monitor on top
// ---------------------------------------------------------------------------
const U = { x0: 18, x1: 232, y0: 34, y1: 168, h: 44 };
box(U.x0, U.y0, 0, U.x1, U.y1, U.h, { right: beige.right, front: beige.front, topFill: beige.top });
{
  const W = U.x1 - U.x0, H = U.h;
  const body = `
  <rect x="10" y="12" width="34" height="7" rx="1.5" fill="url(#accent)" opacity=".8"/>
  <circle cx="16" cy="31" r="2.6" fill="#a6e3a1"/><circle cx="16" cy="31" r="6" fill="#a6e3a1" opacity=".18"/>
  ${[0, 1, 2, 3, 4, 5, 6].map((i) => `<rect x="${58 + i * 6}" y="14" width="2.4" height="18" rx="1.2" fill="${beige.dark}"/>`).join('')}
  ${[8, 25].map((y) => `
  <rect x="${W - 104}" y="${y}" width="92" height="13" rx="1.5" fill="${beige.right}"/>
  <rect x="${W - 96}" y="${y + 5.2}" width="62" height="2.6" rx="1.3" fill="#2a2a35"/>
  <rect x="${W - 28}" y="${y + 3}" width="10" height="7" rx="1" fill="${beige.dark}"/>`).join('')}`;
  out.push(onFront(U.x0, U.y1, H, W, H, body));
}

// CRT: tube (frustum, behind) then bezel box.
const M = { x0: 48, x1: 204, yb: 118, yf: 150, z0: U.h + 4, z1: U.h + 150 };
const T = { x0: 82, x1: 170, y: 52, z0: M.z0 + 26, z1: M.z1 - 30 };
box(96, 100, U.h, 156, 140, M.z0, { right: beige.dark, front: beige.right, topFill: beige.front }); // swivel stand
out.push(poly([iso(M.x1, M.yb, M.z1), iso(T.x1, T.y, T.z1), iso(T.x1, T.y, T.z0), iso(M.x1, M.yb, M.z0)], `fill="${beige.right}"`));
out.push(poly([iso(M.x0, M.yb, M.z1), iso(M.x1, M.yb, M.z1), iso(T.x1, T.y, T.z1), iso(T.x0, T.y, T.z1)], `fill="${beige.top}"`));
// Vents on the tube top.
for (let i = 0; i < 6; i++) {
  const a = iso(T.x0 + 16 + i * 10, T.y + 18, T.z1 + 2 + (M.z1 - T.z1) * 0.22), b = iso(T.x0 + 16 + i * 10, T.y + 44, T.z1 + 2 + (M.z1 - T.z1) * 0.55);
  out.push(`<line x1="${f(a[0])}" y1="${f(a[1])}" x2="${f(b[0])}" y2="${f(b[1])}" stroke="${beige.dark}" stroke-width="2.2" stroke-linecap="round"/>`);
}
box(M.x0, M.yb, M.z0, M.x1, M.yf, M.z1, { right: beige.right, front: beige.front, topFill: beige.top });
{
  const W = M.x1 - M.x0, H = M.z1 - M.z0;
  const sx = 13, sy = 13, sw = W - 26, sh = H - 40;
  const lines = [
    ['C:\\&gt; gca status', '#a6e3a1', 1],
    ['LICENSE ..... ACTIVE', '#a6e3a1', 0.9],
    ['SEATS ...... 11 / 12', '#a6e3a1', 0.75],
    ['UNUSED ...... 0 DAYS', '#a6e3a1', 0.6],
  ];
  const body = `
  <rect x="${sx - 5}" y="${sy - 5}" width="${sw + 10}" height="${sh + 10}" rx="14" fill="${beige.right}"/>
  <rect x="${sx}" y="${sy}" width="${sw}" height="${sh}" rx="11" fill="url(#phosphor)"/>
  <g font-family="ui-monospace, Menlo, Consolas, 'DejaVu Sans Mono', monospace" font-size="9.4" font-weight="600">
    ${lines.map(([t, c, o], i) => `<text x="${sx + 10}" y="${sy + 20 + i * 15}" fill="${c}" opacity="${o}">${t}</text>`).join('')}
    <text x="${sx + 10}" y="${sy + 20 + 4 * 15 + 4}" fill="#a6e3a1">C:\\&gt;</text>
    <rect class="cursor" x="${sx + 40}" y="${sy + 20 + 4 * 15 - 4}" width="6" height="9" fill="#a6e3a1"/>
  </g>
  <rect x="${sx}" y="${sy}" width="${sw}" height="${sh}" rx="11" fill="url(#scan)"/>
  <path d="M${sx + 8} ${sy + 22} Q${sx + 10} ${sy + 8} ${sx + 30} ${sy + 7}" fill="none" stroke="#ffffff" stroke-width="3" stroke-linecap="round" opacity=".12"/>
  <rect x="14" y="${H - 18}" width="26" height="5" rx="2.5" fill="url(#accent)" opacity=".75"/>
  <circle cx="${W - 46}" cy="${H - 15.5}" r="4.4" fill="${beige.dark}"/>
  <circle cx="${W - 32}" cy="${H - 15.5}" r="4.4" fill="${beige.dark}"/>
  <circle cx="${W - 18}" cy="${H - 15.5}" r="2.4" fill="#a6e3a1"/>`;
  out.push(onFront(M.x0, M.yf, M.z1, W, H, body));
}

// ---------------------------------------------------------------------------
// 5. Keyboard (with its coiled cable)
// ---------------------------------------------------------------------------
{
  const a = iso(120, U.y1, 10), b = iso(120, 196, 6);
  out.push(`<path d="M${f(a[0])},${f(a[1])} q-6,4 0,7 q6,3 0,7 q-6,3 0,7 L${f(b[0])},${f(b[1])}" fill="none" stroke="${beige.dark}" stroke-width="2.4" stroke-linecap="round"/>`);
}
const K = { x0: 16, x1: 236, y0: 192, y1: 262, h: 11 };
box(K.x0, K.y0, 0, K.x1, K.y1, K.h, { right: beige.right, front: beige.front, topFill: beige.top });
{
  const cols = 16, rows = 5, kx0 = K.x0 + 8, kx1 = K.x1 - 8, ky0 = K.y0 + 7, ky1 = K.y1 - 7, gap = 2.6;
  const kw = (kx1 - kx0) / cols, kh = (ky1 - ky0) / rows;
  for (let r = 0; r < rows; r++) for (let c = 0; c < cols; c++) {
    let x0 = kx0 + c * kw, x1 = x0 + kw - gap;
    if (r === rows - 1 && c > 3 && c < 11) { if (c !== 4) continue; x1 = kx0 + 11 * kw - gap; } // space bar
    const y0 = ky0 + r * kh, y1 = y0 + kh - gap;
    const fn = c < 2 || c === cols - 1; // function / modifier keys in darker grey
    out.push(poly(top(x0, y0, x1, y1, K.h + 1.6), `fill="${fn ? beige.dark : beige.key}"`));
    out.push(poly([iso(x0, y1, K.h + 1.6), iso(x1, y1, K.h + 1.6), iso(x1, y1, K.h), iso(x0, y1, K.h)], `fill="${fn ? '#7a7366' : beige.right}"`));
  }
  // Enter key in the accent colour.
  const ex0 = kx0 + 14 * kw, ey0 = ky0 + 2 * kh;
  out.push(poly(top(ex0, ey0, ex0 + kw - gap, ey0 + kh - gap, K.h + 1.7), 'fill="#8fcfba"'));
}

// ---------------------------------------------------------------------------
// 6. Mug (accent gradient) with steam
// ---------------------------------------------------------------------------
{
  const cx = 296, cy = 214, r = 17, ch = 40;
  const rx = r * Math.SQRT2 * C, ry = r * Math.SQRT2 * S;
  const [bx, by] = iso(cx, cy, 0), [tx, ty] = iso(cx, cy, ch);
  const [hx, hy] = iso(cx + r + 4, cy, ch * 0.5);
  out.push(`<ellipse cx="${f(hx + 2)}" cy="${f(hy)}" rx="8" ry="10" fill="none" stroke="#7dbb86" stroke-width="4.5"/>`);
  out.push(`<path d="M${f(bx - rx)},${f(ty)} L${f(bx - rx)},${f(by)} A${f(rx)},${f(ry)} 0 0 0 ${f(bx + rx)},${f(by)} L${f(bx + rx)},${f(ty)} Z" fill="url(#mug)"/>`);
  out.push(`<ellipse cx="${f(tx)}" cy="${f(ty)}" rx="${f(rx)}" ry="${f(ry)}" fill="#bfd38a"/>`);
  out.push(`<ellipse cx="${f(tx)}" cy="${f(ty + 1)}" rx="${f(rx - 3)}" ry="${f(ry - 2)}" fill="#3b2f2a"/>`);
  out.push(`<path d="M${f(tx - 5)},${f(ty - 10)} c-12,-15 10,-25 -2,-44 c-8,-13 5,-23 2,-34" fill="none" stroke="url(#steam)" stroke-width="4.5" stroke-linecap="round"/>`);
  out.push(`<path d="M${f(tx + 8)},${f(ty - 13)} c-7,-10 7,-19 0,-31" fill="none" stroke="url(#steam)" stroke-width="3" stroke-linecap="round" opacity=".7"/>`);
}

// ---------------------------------------------------------------------------
const pad = 18;
const vb = [f(minX - pad), f(minY - pad), f(maxX - minX + 2 * pad), f(maxY - minY + 2 * pad)];
const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${vb.join(' ')}" role="img" aria-label="1980s desk with a CRT computer, a keyboard and a basketball poster on the wall">
<!-- Generated by scripts/gen-hero.mjs (make hero) — do not edit by hand. -->
<style>
  .cursor { animation: blink 1.1s steps(1) infinite; }
  @keyframes blink { 50% { opacity: 0; } }
  @media (prefers-reduced-motion: reduce) { .cursor { animation: none; } }
</style>
<defs>
<linearGradient id="accent" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#4fa596"/><stop offset=".55" stop-color="#7dbb86"/><stop offset="1" stop-color="#bfd38a"/></linearGradient>
<linearGradient id="mug" x1="0" y1="0" x2="1" y2="0"><stop offset="0" stop-color="#4fa596"/><stop offset=".6" stop-color="#7dbb86"/><stop offset="1" stop-color="#5d9a77"/></linearGradient>
<radialGradient id="wall"><stop offset="0" stop-color="#cdd6f4" stop-opacity=".06"/><stop offset="1" stop-color="#cdd6f4" stop-opacity="0"/></radialGradient>
<linearGradient id="desk" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#2b2c3f"/><stop offset="1" stop-color="#232435"/></linearGradient>
<linearGradient id="legF" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#232334"/><stop offset="1" stop-color="#232334" stop-opacity="0"/></linearGradient>
<linearGradient id="legR" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#1b1b28"/><stop offset="1" stop-color="#1b1b28" stop-opacity="0"/></linearGradient>
<radialGradient id="glow"><stop offset="0" stop-color="#a6e3a1" stop-opacity=".10"/><stop offset="1" stop-color="#a6e3a1" stop-opacity="0"/></radialGradient>
<radialGradient id="phosphor" cx=".45" cy=".4" r=".75"><stop offset="0" stop-color="#1f3a2d"/><stop offset="1" stop-color="#0d1712"/></radialGradient>
<pattern id="scan" width="4" height="3" patternUnits="userSpaceOnUse"><rect width="4" height="1" fill="#000" opacity=".22"/></pattern>
<pattern id="blocks" width="24" height="12" patternUnits="userSpaceOnUse"><rect width="24" height="12" fill="#bdbdbd"/><path d="M0 11.5H24M12 0V11.5" stroke="#a9a9a9" stroke-width="1"/></pattern>
<clipPath id="photo"><rect x="6" y="6" width="${PW - 12}" height="${PH - 26}"/></clipPath>
<linearGradient id="steam" x1="0" y1="1" x2="0" y2="0"><stop offset="0" stop-color="#9399b2" stop-opacity=".8"/><stop offset="1" stop-color="#9399b2" stop-opacity="0"/></linearGradient>
<filter id="depth" x="-5%" y="-5%" width="110%" height="110%"><feGaussianBlur stdDeviation=".9"/></filter>
</defs>
${out.join('\n')}
</svg>
`;
process.stdout.write(svg);
