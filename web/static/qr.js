/* HomeOwl — a small QR code encoder (byte mode, error correction level M, versions 1–10 → up to 213 bytes).
   Enough for Wi-Fi join codes and short links. No dependencies; renders to SVG. */
"use strict";

const QR = (() => {
  // per version (1–10), level M: [error-correction codewords per block, [blocks, data codewords]…]
  const EC_M = [null, [10, [1, 16]], [16, [1, 28]], [26, [1, 44]], [18, [2, 32]], [24, [2, 43]], [16, [4, 27]], [18, [4, 31]],
    [22, [2, 38], [2, 39]], [22, [3, 36], [2, 37]], [26, [4, 43], [1, 44]]];
  const ALIGN = [null, [], [6, 18], [6, 22], [6, 26], [6, 30], [6, 34], [6, 22, 38], [6, 24, 42], [6, 26, 46], [6, 28, 50]];
  const M_BITS = 0; // level M in the format information

  // GF(256) with the QR polynomial 0x11D
  const EXP = new Array(512), LOG = new Array(256);
  for (let i = 0, x = 1; i < 255; i++) { EXP[i] = x; LOG[x] = i; x <<= 1; if (x & 0x100) x ^= 0x11d; }
  for (let i = 255; i < 512; i++) EXP[i] = EXP[i - 255];
  const mul = (a, b) => a && b ? EXP[LOG[a] + LOG[b]] : 0;
  function rsDivisor(deg) {
    let p = [1];
    for (let i = 0; i < deg; i++) { const n = new Array(p.length + 1).fill(0); p.forEach((c, j) => { n[j] ^= c; n[j + 1] ^= mul(c, EXP[i]); }); p = n; }
    return p.slice(1);
  }
  function rsRemainder(data, div) {
    const r = new Array(div.length).fill(0);
    for (const b of data) { const f = b ^ r.shift(); r.push(0); div.forEach((c, i) => { r[i] ^= mul(c, f); }); }
    return r;
  }

  const dataCap = v => EC_M[v].slice(1).reduce((a, [n, d]) => a + n * d, 0);
  const utf8 = s => Array.from(new TextEncoder().encode(s));

  function codewords(bytes, v) {
    const bits = [], put = (val, len) => { for (let i = len - 1; i >= 0; i--) bits.push((val >>> i) & 1); };
    put(4, 4); put(bytes.length, v < 10 ? 8 : 16); bytes.forEach(b => put(b, 8));
    const cap = dataCap(v) * 8;
    put(0, Math.min(4, cap - bits.length));
    while (bits.length % 8) bits.push(0);
    const data = []; for (let i = 0; i < bits.length; i += 8) data.push(bits.slice(i, i + 8).reduce((a, b) => a << 1 | b, 0));
    for (let p = 0xec; data.length < dataCap(v); p ^= 0xec ^ 0x11) data.push(p);
    // split into blocks, add error correction, interleave
    const ecLen = EC_M[v][0], div = rsDivisor(ecLen), blocks = [];
    let k = 0;
    for (const [n, d] of EC_M[v].slice(1)) for (let i = 0; i < n; i++) { const blk = data.slice(k, k + d); k += d; blocks.push([blk, rsRemainder(blk, div)]); }
    const out = [], maxD = Math.max(...blocks.map(b => b[0].length));
    for (let i = 0; i < maxD; i++) blocks.forEach(b => { if (i < b[0].length) out.push(b[0][i]); });
    for (let i = 0; i < ecLen; i++) blocks.forEach(b => out.push(b[1][i]));
    return out;
  }

  const MASKS = [(x, y) => (x + y) % 2 === 0, (x, y) => y % 2 === 0, x => x % 3 === 0, (x, y) => (x + y) % 3 === 0,
    (x, y) => (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0, (x, y) => x * y % 2 + x * y % 3 === 0,
    (x, y) => (x * y % 2 + x * y % 3) % 2 === 0, (x, y) => ((x + y) % 2 + x * y % 3) % 2 === 0];

  function build(v, cw, mask) {
    const n = v * 4 + 17, m = Array.from({ length: n }, () => new Array(n).fill(false)), fn = Array.from({ length: n }, () => new Array(n).fill(false));
    const set = (x, y, d) => { m[y][x] = d; fn[y][x] = true; };
    for (let i = 0; i < n; i++) { set(6, i, i % 2 === 0); set(i, 6, i % 2 === 0); }
    for (const [cx, cy] of [[3, 3], [n - 4, 3], [3, n - 4]])
      for (let dy = -4; dy <= 4; dy++) for (let dx = -4; dx <= 4; dx++) {
        const x = cx + dx, y = cy + dy, d = Math.max(Math.abs(dx), Math.abs(dy));
        if (x >= 0 && x < n && y >= 0 && y < n) set(x, y, d !== 2 && d !== 4);
      }
    const al = ALIGN[v], last = al.length - 1;
    al.forEach((ax, i) => al.forEach((ay, j) => {
      if ((i === 0 && j === 0) || (i === 0 && j === last) || (i === last && j === 0)) return;
      for (let dy = -2; dy <= 2; dy++) for (let dx = -2; dx <= 2; dx++) set(ax + dx, ay + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
    }));
    format(m, set, n, 0); // reserve the format areas (real bits drawn after masking)
    if (v >= 7) {
      let r = v; for (let i = 0; i < 12; i++) r = (r << 1) ^ ((r >>> 11) * 0x1f25);
      const bits = v << 12 | r;
      for (let i = 0; i < 18; i++) { const b = ((bits >>> i) & 1) === 1, a = n - 11 + i % 3, c = Math.floor(i / 3); set(a, c, b); set(c, a, b); }
    }
    // data, in the zig-zag order
    let i = 0;
    for (let right = n - 1; right >= 1; right -= 2) {
      if (right === 6) right = 5;
      for (let vert = 0; vert < n; vert++) for (let j = 0; j < 2; j++) {
        const x = right - j, up = ((right + 1) & 2) === 0, y = up ? n - 1 - vert : vert;
        if (!fn[y][x] && i < cw.length * 8) { m[y][x] = ((cw[i >>> 3] >>> (7 - (i & 7))) & 1) === 1; i++; }
      }
    }
    for (let y = 0; y < n; y++) for (let x = 0; x < n; x++) if (!fn[y][x] && MASKS[mask](x, y)) m[y][x] = !m[y][x];
    format(m, set, n, mask);
    return m;
  }

  function format(m, set, n, mask) {
    const data = M_BITS << 3 | mask;
    let r = data; for (let i = 0; i < 10; i++) r = (r << 1) ^ ((r >>> 9) * 0x537);
    const bits = (data << 10 | r) ^ 0x5412, b = i => ((bits >>> i) & 1) === 1;
    for (let i = 0; i <= 5; i++) set(8, i, b(i));
    set(8, 7, b(6)); set(8, 8, b(7)); set(7, 8, b(8));
    for (let i = 9; i < 15; i++) set(14 - i, 8, b(i));
    for (let i = 0; i < 8; i++) set(n - 1 - i, 8, b(i));
    for (let i = 8; i < 15; i++) set(8, n - 15 + i, b(i));
    set(8, n - 8, true);
  }

  function penalty(m) {
    const n = m.length; let p = 0, dark = 0;
    const lines = [];
    for (let i = 0; i < n; i++) { lines.push(m[i]); lines.push(m.map(r => r[i])); }
    for (const l of lines) {
      let run = 1;
      for (let i = 1; i <= n; i++) { if (i < n && l[i] === l[i - 1]) run++; else { if (run >= 5) p += run - 2; run = 1; } }
      const s = l.map(x => x ? 1 : 0).join("");
      for (const pat of ["10111010000", "00001011101"]) { let k = s.indexOf(pat); while (k >= 0) { p += 40; k = s.indexOf(pat, k + 1); } }
    }
    for (let y = 0; y < n - 1; y++) for (let x = 0; x < n - 1; x++) { const c = m[y][x]; if (c === m[y][x + 1] && c === m[y + 1][x] && c === m[y + 1][x + 1]) p += 3; }
    m.forEach(r => r.forEach(c => { if (c) dark++; }));
    return p + Math.floor(Math.abs(dark * 20 - n * n * 10) / (n * n)) * 10;
  }

  // encode(text) → boolean matrix (true = dark). Throws when the text is too long.
  function encode(text) {
    const bytes = utf8(text);
    let v = 1;
    while (v <= 10 && 4 + (v < 10 ? 8 : 16) + bytes.length * 8 > dataCap(v) * 8) v++;
    if (v > 10) throw new Error("too long for a QR code");
    const cw = codewords(bytes, v);
    let best = null, bestP = Infinity;
    for (let mask = 0; mask < 8; mask++) { const m = build(v, cw, mask), p = penalty(m); if (p < bestP) { best = m; bestP = p; } }
    return best;
  }

  function svg(text, { size = 220, label = "QR code" } = {}) {
    const m = encode(text), n = m.length, q = 4, w = n + q * 2;
    let d = "";
    m.forEach((r, y) => r.forEach((c, x) => { if (c) d += `M${x + q} ${y + q}h1v1h-1z`; }));
    return `<svg class="qr" viewBox="0 0 ${w} ${w}" width="${size}" height="${size}" role="img" aria-label="${esc(label)}" shape-rendering="crispEdges"><rect width="${w}" height="${w}" fill="#fff"/><path d="${d}" fill="#000"/></svg>`;
  }

  // Wi-Fi join code (understood by the iPhone and Android cameras)
  const wifiEsc = s => String(s ?? "").replace(/([\\;,:"])/g, "\\$1");
  function wifi({ ssid, password, security = "WPA", hidden = false }) {
    const t = !password || security === "nopass" ? "nopass" : security;
    return `WIFI:T:${t};S:${wifiEsc(ssid)};${t === "nopass" ? "" : `P:${wifiEsc(password)};`}${hidden ? "H:true;" : ""};`;
  }
  return { encode, svg, wifi };
})();
