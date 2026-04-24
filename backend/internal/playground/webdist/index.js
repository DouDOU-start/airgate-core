import { jsxs as l, jsx as r, Fragment as fe } from "react/jsx-runtime";
import { useState as w, useRef as Y, useEffect as F, useCallback as R } from "react";
import { useTranslation as ht } from "react-i18next";
const Fe = {
  primary: "#3ecfb4",
  primaryHover: "#62dcc4",
  primarySubtle: "rgba(62, 207, 180, 0.08)",
  primaryGlow: "rgba(62, 207, 180, 0.14)",
  success: "#34d399",
  successSubtle: "rgba(52, 211, 153, 0.12)",
  warning: "#fbbf24",
  warningSubtle: "rgba(251, 191, 36, 0.12)",
  danger: "#fb7185",
  dangerSubtle: "rgba(251, 113, 133, 0.12)",
  info: "#7dd3fc",
  infoSubtle: "rgba(125, 211, 252, 0.12)",
  // 背景：深蓝黑，带微蓝底调增加深度感
  bgDeep: "#06080e",
  bg: "#0c0f17",
  bgElevated: "#131722",
  bgSurface: "#1a1e2a",
  bgHover: "#232836",
  bgActive: "#2c3240",
  // 边框：蓝调透明
  border: "rgba(148, 175, 225, 0.08)",
  borderSubtle: "rgba(148, 175, 225, 0.05)",
  borderFocus: "rgba(62, 207, 180, 0.40)",
  // 文字：微蓝白，长时间阅读更舒适
  text: "#e2e6f0",
  textSecondary: "#8d93a8",
  textTertiary: "#565d73",
  textInverse: "#06080e",
  glass: "rgba(148, 175, 225, 0.03)",
  glassBorder: "rgba(148, 175, 225, 0.06)",
  shadowSm: "0 2px 8px rgba(0, 0, 0, 0.36)",
  shadowMd: "0 8px 24px rgba(0, 0, 0, 0.48)",
  shadowLg: "0 20px 48px rgba(0, 0, 0, 0.60)",
  shadowGlow: "0 0 0 1px rgba(62, 207, 180, 0.08), 0 8px 32px rgba(62, 207, 180, 0.10)"
}, mt = {
  radiusSm: "12px",
  radiusMd: "18px",
  radiusLg: "22px",
  radiusXl: "28px",
  fontSans: "'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif",
  fontMono: "'JetBrains Mono', 'SF Mono', 'Cascadia Code', monospace",
  transition: "200ms cubic-bezier(0.4, 0, 0.2, 1)",
  transitionSlow: "400ms cubic-bezier(0.4, 0, 0.2, 1)"
}, ft = {
  sidebarWidth: "260px",
  sidebarCollapsed: "72px",
  topbarHeight: "64px"
}, Te = {
  ...mt,
  ...ft
}, Ke = {
  dark: Fe
};
function yt(e) {
  return e.replace(/[A-Z]/g, (o) => "-" + o.toLowerCase());
}
function Ne(e = "ag") {
  return e.trim() || "ag";
}
function xe(e, o) {
  return `--${e}-${yt(o)}`;
}
Object.keys(Ke.dark).reduce((e, o) => (e[o] = xe("ag", o), e), {});
Object.keys(Te).reduce((e, o) => (e[o] = xe("ag", o), e), {});
function Ue(e = {}) {
  const o = Ne(e.prefix);
  return Object.keys(Ke.dark).reduce((s, a) => (s[a] = xe(o, a), s), {});
}
function Ve(e = {}) {
  const o = Ne(e.prefix);
  return Object.keys(Te).reduce((s, a) => (s[a] = xe(o, a), s), {});
}
const xt = Ue(), bt = Ve();
function i(e, o = {}) {
  const s = o.prefix ? Ue(o) : xt, a = o.prefix ? Ve(o) : bt;
  if (e in s) {
    const c = e;
    return `var(${s[c]}, ${Fe[c]})`;
  }
  const u = e;
  return `var(${a[u]}, ${Te[u]})`;
}
const vt = "/api/v1/ext-user/airgate-playground", wt = "/api/v1";
function kt() {
  const e = {}, o = localStorage.getItem("token");
  return o && (e.Authorization = `Bearer ${o}`), e;
}
async function N(e, o, s, a = vt) {
  const u = { ...kt() };
  s !== void 0 && (u["Content-Type"] = "application/json");
  const c = await fetch(a + o, {
    method: e,
    headers: u,
    body: s ? JSON.stringify(s) : void 0
  });
  if (!c.ok) {
    const y = await c.text();
    let x = `HTTP ${c.status}`;
    try {
      const v = JSON.parse(y);
      x = v.error || v.message || x;
    } catch {
    }
    throw c.status === 401 && (localStorage.removeItem("token"), window.location.href = "/login"), new Error(x);
  }
  const p = await c.text();
  return p ? JSON.parse(p) : null;
}
async function ye(e, o, s) {
  const a = await N(e, o, s, wt);
  if (a.code !== 0)
    throw new Error(a.message || "request failed");
  return a.data;
}
const H = {
  listConversations: () => N("GET", "/conversations"),
  createConversation: (e) => N("POST", "/conversations", e),
  getConversation: (e) => N("GET", `/conversations/${e}`),
  updateConversation: (e, o) => N("PUT", `/conversations/${e}`, o),
  deleteConversation: (e) => N("DELETE", `/conversations/${e}`),
  listMessages: (e) => N("GET", `/messages/${e}`),
  persistMessage: (e) => N("POST", "/messages", e),
  listModelsByAPIKey: async (e) => {
    var u;
    const o = await fetch("/v1/models", {
      headers: { Authorization: `Bearer ${e}` }
    });
    if (!o.ok) {
      const c = await o.text();
      let p = `HTTP ${o.status}`;
      try {
        const y = JSON.parse(c);
        p = ((u = y.error) == null ? void 0 : u.message) || y.error || y.message || p;
      } catch {
      }
      throw new Error(p);
    }
    const s = await o.json();
    return (Array.isArray(s) ? s : s.data || []).map((c) => ({
      id: c.id,
      name: c.name || c.id,
      input_price: c.input_price || 0,
      output_price: c.output_price || 0,
      context_window: c.context_window || 0,
      max_output_tokens: c.max_output_tokens || 0,
      image_only: !!c.image_only,
      capabilities: c.capabilities || []
    }));
  },
  getUserInfo: () => ye("GET", "/users/me"),
  listAPIKeys: () => ye("GET", "/api-keys?page=1&page_size=100"),
  revealAPIKey: (e) => ye("GET", `/api-keys/${e}/reveal`),
  listGroups: () => ye("GET", "/groups?page=1&page_size=100")
};
async function St(e, o, s, a) {
  var _, k, T;
  const u = {
    ...o,
    stream_options: {
      include_usage: !0,
      ...o.stream_options
    }
  }, c = await fetch("/v1/chat/completions", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "text/event-stream",
      Authorization: `Bearer ${e}`
    },
    body: JSON.stringify(u),
    signal: a
  });
  if (!c.ok || !c.body) {
    const C = await c.text();
    let S = `HTTP ${c.status}`;
    try {
      const $ = JSON.parse(C);
      S = ((_ = $.error) == null ? void 0 : _.message) || $.error || $.message || S;
    } catch {
    }
    s.onError(S);
    return;
  }
  const p = c.body.getReader(), y = new TextDecoder();
  let x = "", v = { input_tokens: 0, output_tokens: 0, model: o.model, cost: 0 };
  try {
    for (; ; ) {
      const { done: C, value: S } = await p.read();
      if (C) break;
      x += y.decode(S, { stream: !0 });
      const $ = x.split(`
`);
      x = $.pop() || "";
      for (const f of $) {
        const L = f.trim();
        if (!L.startsWith("data: ")) continue;
        const E = L.slice(6);
        if (E === "[DONE]") {
          s.onDone(v);
          return;
        }
        try {
          const I = JSON.parse(E);
          if (I.error) {
            s.onError(I.error.message || I.error);
            return;
          }
          const D = (T = (k = I.choices) == null ? void 0 : k[0]) == null ? void 0 : T.delta, M = D == null ? void 0 : D.reasoning_content;
          M && s.onReasoning(M);
          const W = D == null ? void 0 : D.content;
          W && s.onData(W), I.usage && (v = {
            input_tokens: I.usage.prompt_tokens || I.usage.input_tokens || 0,
            output_tokens: I.usage.completion_tokens || I.usage.output_tokens || 0,
            model: I.model || v.model,
            cost: I.usage.cost || 0
          });
        } catch {
        }
      }
    }
    s.onDone(v);
  } catch (C) {
    if (a != null && a.aborted) return;
    s.onError(C instanceof Error ? C.message : "stream failed");
  }
}
const je = 960, K = -1, le = /!\[[^\]]*\]\((data:image\/(?:png|jpeg|jpg|webp|gif);base64,[^)]+|https?:\/\/[^\s)]+)\)/g, _e = /!\[([^\]]*)\]\((data:image\/(?:png|jpeg|jpg|webp|gif);base64,[^)]+|https?:\/\/[^\s)]+)\)/g, qe = /^data:image\/(png|jpeg|jpg|webp|gif);base64,/i, Mt = /(^|[-_])(?:gpt-?5|o[134]|codex)(?:[-_.]|$)/i, It = 10 * 1024 * 1024, _t = [
  { value: "1024x1024", label: "Square 1024x1024" },
  { value: "1024x1536", label: "Portrait 1024x1536" },
  { value: "1536x1024", label: "Landscape 1536x1024" }
];
function Ct(e) {
  return e.replace(le, "[Image generated]").trim() || "[Image generated]";
}
function Pe(e) {
  return e.replace(le, "[Image]").trim() || "[Image]";
}
function Bt(e) {
  return e.replace(/[\]\\]/g, "");
}
function Tt(e) {
  const o = e.match(qe);
  if (o) return o[1].toLowerCase() === "jpeg" ? "jpg" : o[1].toLowerCase();
  try {
    const a = new URL(e).pathname.match(/\.([a-z0-9]{2,5})$/i);
    return a ? a[1].toLowerCase() : "png";
  } catch {
    return "png";
  }
}
function Lt(e, o) {
  return `${(e || "generated-image").replace(/\.[a-z0-9]{2,5}$/i, "").replace(/[\\/:*?"<>|]+/g, "-").trim().slice(0, 80) || "generated-image"}.${Tt(o)}`;
}
function Ce(e, o) {
  const s = document.createElement("a");
  s.href = e, s.download = o, s.rel = "noreferrer", document.body.appendChild(s), s.click(), s.remove();
}
async function Rt(e, o) {
  const s = Lt(o, e);
  if (qe.test(e)) {
    Ce(e, s);
    return;
  }
  try {
    const a = await fetch(e);
    if (!a.ok) throw new Error(`HTTP ${a.status}`);
    const u = URL.createObjectURL(await a.blob());
    try {
      Ce(u, s);
    } finally {
      URL.revokeObjectURL(u);
    }
  } catch {
    Ce(e, s);
  }
}
async function $t(e) {
  var a;
  if ((a = navigator.clipboard) != null && a.writeText && window.isSecureContext) {
    await navigator.clipboard.writeText(e);
    return;
  }
  const o = document.createElement("textarea");
  o.value = e, o.style.position = "fixed", o.style.opacity = "0", o.style.pointerEvents = "none", document.body.appendChild(o), o.select();
  const s = document.execCommand("copy");
  if (o.remove(), !s) throw new Error("copy failed");
}
function Dt(e) {
  return new Promise((o, s) => {
    const a = new FileReader();
    a.onload = () => o(String(a.result || "")), a.onerror = () => s(a.error || new Error("Failed to read image")), a.readAsDataURL(e);
  });
}
function At(e, o) {
  const s = e.trim(), a = o.map((u) => `![${Bt(u.name)}](${u.url})`).join(`
`);
  return [s, a].filter(Boolean).join(`

`);
}
async function zt(e) {
  const o = e.filter((s) => s.type.startsWith("image/"));
  if (o.some((s) => s.size > It))
    throw new Error("Images must be 10MB or smaller");
  return Promise.all(o.map(async (s) => ({
    id: `${s.name}-${s.lastModified}-${s.size}`,
    name: s.name || "pasted-image",
    url: await Dt(s)
  })));
}
function Et(e) {
  const o = e.replace(le, "[Image]").trim() || "[Image]";
  return o.slice(0, 30) + (o.length > 30 ? "..." : "");
}
function Ht(e, o) {
  if (e !== "user") return Ct(o);
  const s = [];
  let a = 0, u;
  for (le.lastIndex = 0; (u = le.exec(o)) !== null; ) {
    const p = o.slice(a, u.index).trim();
    p && s.push({ type: "text", text: p }), s.push({ type: "image_url", image_url: { url: u[1] } }), a = u.index + u[0].length;
  }
  const c = o.slice(a).trim();
  return c && s.push({ type: "text", text: c }), s.length ? s : o;
}
function Be(e) {
  var o;
  return !!(e != null && e.image_only || (o = e == null ? void 0 : e.capabilities) != null && o.includes("image_generation"));
}
function Ge(e) {
  var o, s;
  return !e || Be(e) ? !1 : !!((o = e.capabilities) != null && o.includes("reasoning") || (s = e.capabilities) != null && s.includes("thinking") || Mt.test(e.id));
}
function Wt(e) {
  return /^(https?:|mailto:|#)/i.test(e);
}
function jt(e) {
  return /^(data:image\/(?:png|jpeg|jpg|webp|gif);base64,|https?:)/i.test(e);
}
function Oe(e, o, s) {
  o.split(`
`).forEach((u, c) => {
    c > 0 && e.push(/* @__PURE__ */ r("br", {}, `${s}-br-${c}`)), u && e.push(u);
  });
}
function Je(e, o, s, a) {
  const u = /* @__PURE__ */ r("img", { src: o, alt: s, style: n.generatedImage, loading: "lazy" });
  return a.onImageDownload ? /* @__PURE__ */ l("span", { style: n.generatedImageFrame, children: [
    u,
    /* @__PURE__ */ l(
      "button",
      {
        type: "button",
        style: n.imageDownloadBtn,
        title: a.imageDownloadTitle || "Download image",
        "aria-label": a.imageDownloadTitle || "Download image",
        onClick: (c) => {
          var p;
          c.stopPropagation(), (p = a.onImageDownload) == null || p.call(a, o, s);
        },
        children: [
          /* @__PURE__ */ l("svg", { width: "15", height: "15", viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: "2", strokeLinecap: "round", strokeLinejoin: "round", children: [
            /* @__PURE__ */ r("path", { d: "M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" }),
            /* @__PURE__ */ r("path", { d: "M7 10l5 5 5-5" }),
            /* @__PURE__ */ r("path", { d: "M12 15V3" })
          ] }),
          /* @__PURE__ */ r("span", { children: "Download" })
        ]
      }
    )
  ] }, e) : /* @__PURE__ */ r("span", { style: n.generatedImageFrame, children: u }, e);
}
function X(e, o, s = {}) {
  const a = [], u = /(!\[([^\]]*)\]\(([^)\s]+)\)|\[([^\]]+)\]\(([^)\s]+)\)|`([^`]+)`|\*\*([^*]+)\*\*|__([^_]+)__|\*([^*]+)\*|_([^_]+)_)/g;
  let c = 0, p;
  for (; (p = u.exec(e)) !== null; ) {
    p.index > c && Oe(a, e.slice(c, p.index), `${o}-text-${c}`);
    const y = `${o}-${p.index}`, x = p[2], v = p[3], _ = p[4], k = p[5], T = p[6], C = p[7] || p[8], S = p[9] || p[10];
    v && jt(v) ? a.push(Je(y, v, x || "Generated image", s)) : k && Wt(k) ? a.push(
      /* @__PURE__ */ r("a", { href: k, style: n.markdownLink, target: "_blank", rel: "noreferrer", children: X(_, `${y}-link`, s) }, y)
    ) : T ? a.push(/* @__PURE__ */ r("code", { style: n.markdownInlineCode, children: T }, y)) : C ? a.push(/* @__PURE__ */ r("strong", { children: X(C, `${y}-bold`, s) }, y)) : S ? a.push(/* @__PURE__ */ r("em", { children: X(S, `${y}-em`, s) }, y)) : a.push(p[0]), c = p.index + p[0].length;
  }
  return c < e.length && Oe(a, e.slice(c), `${o}-text-${c}`), a.length > 0 ? a : e;
}
function Pt(e, o, s, a = {}) {
  const u = X(o, `${s}-inline`, a);
  return e === 1 ? /* @__PURE__ */ r("h1", { style: n.markdownH1, children: u }, s) : e === 2 ? /* @__PURE__ */ r("h2", { style: n.markdownH2, children: u }, s) : e === 3 ? /* @__PURE__ */ r("h3", { style: n.markdownH3, children: u }, s) : /* @__PURE__ */ r("h4", { style: n.markdownH4, children: u }, s);
}
function Gt(e, o, s = {}) {
  const a = [];
  let u;
  for (_e.lastIndex = 0; (u = _e.exec(e)) !== null; )
    a.push({ alt: u[1], url: u[2] });
  const c = e.replace(_e, "").trim();
  return !a.length || c ? null : /* @__PURE__ */ r("div", { style: n.imageGroup, children: a.map((p, y) => Je(`${o}-${y}`, p.url, p.alt || "Generated image", s)) }, o);
}
function se(e, o = {}) {
  const s = e.replace(/\r\n?/g, `
`).split(`
`), a = [];
  let u = [], c = [], p = [], y = [], x = !1, v = 0;
  const _ = (f) => `${f}-${v++}`, k = () => {
    if (!u.length) return;
    const f = _("p"), L = u.join(`
`);
    a.push(Gt(L, f, o) || /* @__PURE__ */ r("p", { style: n.markdownParagraph, children: X(L, f, o) }, f)), u = [];
  }, T = () => {
    if (!c.length) return;
    const f = _("quote");
    a.push(/* @__PURE__ */ r("blockquote", { style: n.markdownBlockquote, children: X(c.join(`
`), f, o) }, f)), c = [];
  }, C = () => {
    if (!p.length) return;
    const f = _("list"), L = p.map((E, I) => /* @__PURE__ */ r("li", { style: n.markdownListItem, children: X(E.text, `${f}-${I}`, o) }, `${f}-${I}`));
    a.push(p[0].ordered ? /* @__PURE__ */ r("ol", { style: n.markdownList, children: L }, f) : /* @__PURE__ */ r("ul", { style: n.markdownList, children: L }, f)), p = [];
  }, S = () => {
    k(), T(), C();
  }, $ = () => {
    const f = _("code");
    a.push(/* @__PURE__ */ r("pre", { style: n.markdownCodeBlock, children: /* @__PURE__ */ r("code", { children: y.join(`
`) }) }, f)), y = [];
  };
  for (const f of s) {
    if (f.match(/^```/)) {
      x ? ($(), x = !1) : (S(), x = !0);
      continue;
    }
    if (x) {
      y.push(f);
      continue;
    }
    if (!f.trim()) {
      S();
      continue;
    }
    const E = f.match(/^(#{1,6})\s+(.+)$/);
    if (E) {
      S(), a.push(Pt(Math.min(E[1].length, 4), E[2].trim(), _("heading"), o));
      continue;
    }
    if (/^\s*([-*_])\s*(\1\s*){2,}$/.test(f)) {
      S(), a.push(/* @__PURE__ */ r("hr", { style: n.markdownDivider }, _("hr")));
      continue;
    }
    const I = f.match(/^>\s?(.*)$/);
    if (I) {
      k(), C(), c.push(I[1]);
      continue;
    }
    const D = f.match(/^\s*[-*+]\s+(.+)$/), M = f.match(/^\s*\d+[.)]\s+(.+)$/);
    if (D || M) {
      k(), T();
      const W = !!M;
      p.length && p[0].ordered !== W && C(), p.push({ ordered: W, text: ((M == null ? void 0 : M[1]) || (D == null ? void 0 : D[1]) || "").trim() });
      continue;
    }
    T(), C(), u.push(f);
  }
  return x && $(), S(), a.length > 0 ? a : e;
}
function Ot() {
  const { t: e } = ht(), [o, s] = w([]), [a, u] = w(null), [c, p] = w([]), [y, x] = w(null), [v, _] = w(""), [k, T] = w(""), [C, S] = w(!1), [$, f] = w(""), [L, E] = w([]), [I, D] = w([]), [M, W] = w(""), [be, Ye] = w("medium"), [ve, Xe] = w("1024x1024"), [h, Qe] = w(null), [de, Ze] = w([]), [et, tt] = w([]), [z, Le] = w(null), [Q, Z] = w(""), [Re, P] = w(""), [ce, ee] = w(""), [pe, te] = w(!0), [m, nt] = w(() => typeof window < "u" ? window.innerWidth <= je : !1), $e = Y(null), De = Y(null), we = Y(null), ue = Y(null), G = Y(null), O = Y(null), ke = Y(null);
  F(() => {
    H.listConversations().then(s).catch(() => {
    }), H.getUserInfo().then(async (t) => {
      Qe(t);
      const d = sessionStorage.getItem("apikey_session_secret") || "";
      t.api_key_id && d && (Le(t.api_key_id), Z(d));
    }).catch(() => {
    }), H.listAPIKeys().then((t) => Ze(t.list.filter((d) => d.status === "active" && d.group_id != null))).catch(() => {
    }), H.listGroups().then((t) => tt(t.list)).catch(() => {
    });
  }, []), F(() => {
    let t = !1;
    return (async () => {
      const g = h != null && h.api_key_id && (!z || z === h.api_key_id) && sessionStorage.getItem("apikey_session_secret") || "";
      let B = Q || g;
      try {
        if (!B && z && (B = (await H.revealAPIKey(z)).key || "", t || Z(B)), !B) {
          t || (D([]), W(""));
          return;
        }
        const A = await H.listModelsByAPIKey(B);
        if (t) return;
        D(A), W((j) => {
          var q;
          return A.some((oe) => oe.id === j) ? j : ((q = A[0]) == null ? void 0 : q.id) || "";
        });
      } catch (A) {
        if (t) return;
        D([]), W(""), P(A instanceof Error ? A.message : "Failed to load models");
      }
    })(), () => {
      t = !0;
    };
  }, [Q, z, h == null ? void 0 : h.api_key_id]), F(() => {
    G.current = a;
  }, [a]), F(() => {
    if (!a || a === K) {
      p([]);
      return;
    }
    if (ke.current === a) {
      ke.current = null;
      return;
    }
    H.listMessages(a).then(p).catch(() => {
    });
  }, [a]), F(() => {
    var t;
    (t = $e.current) == null || t.scrollIntoView({ behavior: "smooth" });
  }, [c, v, k]), F(() => {
    if (typeof window > "u") return;
    const t = window.matchMedia(`(max-width: ${je}px)`), d = (g) => {
      nt(g ? g.matches : t.matches);
    };
    return d(), t.addEventListener ? (t.addEventListener("change", d), () => t.removeEventListener("change", d)) : (t.addListener(d), () => t.removeListener(d));
  }, []), F(() => {
    te(!m);
  }, [m]), F(() => {
    if (!ce) return;
    const t = window.setTimeout(() => ee(""), 1400);
    return () => window.clearTimeout(t);
  }, [ce]);
  const ne = R(() => {
    const t = z || (h == null ? void 0 : h.api_key_id), d = de.find((g) => g.id === t);
    return (d == null ? void 0 : d.group_id) || 0;
  }, [de, z, h]), U = (() => {
    var d;
    const t = ne();
    return t ? ((d = et.find((g) => g.id === t)) == null ? void 0 : d.platform) || "" : (h == null ? void 0 : h.api_key_platform) || "";
  })(), Ae = I.find((t) => t.id === M), Se = Be(Ae), Me = Ge(Ae), ze = R(async () => {
    if (Q) return Q;
    if (h != null && h.api_key_id && (!z || z === h.api_key_id)) {
      const d = sessionStorage.getItem("apikey_session_secret") || "";
      if (d)
        return Z(d), d;
    }
    if (!z)
      throw new Error("API key required");
    const t = await H.revealAPIKey(z);
    if (!t.key)
      throw new Error("Failed to reveal API key");
    return Z(t.key), t.key;
  }, [Q, z, h]), Ee = R(() => {
    const t = (/* @__PURE__ */ new Date()).toISOString(), d = {
      id: K,
      user_id: (h == null ? void 0 : h.id) || 0,
      title: "",
      group_id: ne(),
      platform: U,
      model: M,
      created_at: t,
      updated_at: t
    };
    s((g) => [d, ...g.filter((B) => B.id !== K)]), u(K), p([]), E([]), P(""), m && te(!1);
  }, [m, ne, U, M, h == null ? void 0 : h.id]), rt = R(async (t) => {
    var g, B;
    if (await ((B = (g = window.airgate) == null ? void 0 : g.confirm) == null ? void 0 : B.call(g, e("playground.delete_conversation_confirm"), {
      title: e("playground.delete_conversation"),
      danger: !0
    }))) {
      if (t === K) {
        s((A) => A.filter((j) => j.id !== t)), a === t && (u(null), p([]));
        return;
      }
      try {
        await H.deleteConversation(t), s((A) => A.filter((j) => j.id !== t)), a === t && (u(null), p([]));
      } catch {
      }
    }
  }, [a, e]), Ie = R(async () => {
    if (!$.trim() && L.length === 0 || C || !a) return;
    const t = At($, L), d = ne();
    let g = a;
    const B = [...c, {
      id: Date.now(),
      conversation_id: a,
      role: "user",
      content: t,
      platform: U,
      model: M,
      group_id: d,
      input_tokens: 0,
      output_tokens: 0,
      cost: 0,
      created_at: (/* @__PURE__ */ new Date()).toISOString()
    }];
    f(""), E([]), we.current && (we.current.style.height = "24px"), ue.current && (ue.current.value = ""), P(""), p(B), S(!0), x(g), O.current = { conversationId: g, model: M }, _(""), T("");
    try {
      const A = await ze();
      if (g === K) {
        const b = await H.createConversation({
          title: "",
          group_id: d,
          platform: U,
          model: M
        });
        g = b.id, O.current = { conversationId: b.id, model: M }, x(b.id), G.current === K && (G.current = b.id, ke.current = b.id, u(b.id), p((ae) => ae.map((J) => ({ ...J, conversation_id: b.id })))), s((ae) => [b, ...ae.filter((J) => J.id !== K)]);
      }
      await H.persistMessage({
        conversation_id: g,
        role: "user",
        content: t,
        platform: U,
        model: M,
        group_id: d
      });
      const j = new AbortController();
      De.current = j;
      let q = "", oe = "";
      await St(
        A,
        {
          model: M,
          messages: B.map((b) => ({ role: b.role, content: Ht(b.role, b.content) })),
          stream: !0,
          ...Se ? { size: ve } : {},
          ...Me ? { reasoning_effort: be } : {}
        },
        {
          onData: (b) => {
            q += b, _(q);
          },
          onReasoning: (b) => {
            oe += b, T(oe);
          },
          onDone: async (b) => {
            if (!q) {
              G.current === g && P(e("playground.no_response")), _(""), T(""), x(null), O.current = null, S(!1);
              return;
            }
            const ae = await H.persistMessage({
              conversation_id: g,
              role: "assistant",
              content: q,
              reasoning: oe,
              platform: U,
              model: b.model || M,
              group_id: d,
              input_tokens: b.input_tokens,
              output_tokens: b.output_tokens,
              cost: b.cost
            });
            G.current === g && p((J) => [...J, ae]), s((J) => J.map(
              (me) => me.id === g && !me.title ? { ...me, title: Et(t), updated_at: (/* @__PURE__ */ new Date()).toISOString() } : me
            )), _(""), T(""), x(null), O.current = null, S(!1);
          },
          onError: (b) => {
            G.current === g && P(b), S(!1), _(""), T(""), x(null), O.current = null;
          }
        },
        j.signal
      );
    } catch (A) {
      G.current === g && P(A instanceof Error ? A.message : "stream failed"), S(!1), _(""), T(""), x(null), O.current = null;
    }
  }, [a, ze, ve, $, C, c, L, be, ne, M, U, Se, Me, e]), ge = R(async (t) => {
    if (t.length)
      try {
        const d = await zt(t);
        if (!d.length) return;
        E((g) => [...g, ...d]), P("");
      } catch (d) {
        P(d instanceof Error ? d.message : "Failed to read image");
      }
  }, []), it = R(async (t) => {
    await ge(Array.from(t.target.files || [])), t.target.value = "";
  }, [ge]), ot = R((t) => {
    const d = Array.from(t.clipboardData.items).filter((g) => g.kind === "file" && g.type.startsWith("image/")).map((g) => g.getAsFile()).filter((g) => !!g);
    d.length && ge(d);
  }, [ge]), at = R((t) => {
    E((d) => d.filter((g) => g.id !== t));
  }, []), st = R(() => {
    var d;
    (d = De.current) == null || d.abort();
    const t = O.current;
    if (v || k) {
      const g = t == null ? void 0 : t.conversationId;
      g && G.current === g && p((B) => [...B, {
        id: Date.now() + 1,
        conversation_id: g,
        role: "assistant",
        content: v,
        reasoning: k,
        platform: "",
        model: t.model,
        group_id: 0,
        input_tokens: 0,
        output_tokens: 0,
        cost: 0,
        created_at: (/* @__PURE__ */ new Date()).toISOString()
      }]);
    }
    _(""), T(""), x(null), O.current = null, S(!1);
  }, [v, k]), He = o.find((t) => t.id === a), V = C && y === a, he = !!(h != null && h.api_key_id || z), We = !!($.trim() || L.length > 0) && he && !C, lt = R((t) => {
    if (t.key === "Enter" && !t.shiftKey) {
      if (t.preventDefault(), !he) {
        P("API key required");
        return;
      }
      Ie();
    }
  }, [he, Ie]), dt = R(() => {
    var t;
    (t = ue.current) == null || t.click();
  }, []), ct = R((t) => {
    t.style.height = "auto", t.style.height = Math.min(t.scrollHeight, 200) + "px";
  }, []), pt = R((t) => {
    u(t), m && te(!1);
  }, [m]), ut = R((t, d) => {
    Rt(t, d).then(() => ee("Image download started")).catch(() => ee("Image download failed"));
  }, []), gt = R((t) => {
    $t(t).then(() => ee("Message copied")).catch(() => ee("Copy failed"));
  }, []), re = {
    onImageDownload: ut,
    imageDownloadTitle: "Download image"
  }, ie = (t, d = "Copy message", g = !1) => /* @__PURE__ */ r(
    "button",
    {
      type: "button",
      style: n.messageCopyBtn,
      title: d,
      "aria-label": d,
      onClick: (B) => {
        g && (B.preventDefault(), B.stopPropagation()), gt(t);
      },
      children: /* @__PURE__ */ l("svg", { width: "13", height: "13", viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: "2", strokeLinecap: "round", strokeLinejoin: "round", children: [
        /* @__PURE__ */ r("rect", { x: "9", y: "9", width: "13", height: "13", rx: "2", ry: "2" }),
        /* @__PURE__ */ r("path", { d: "M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" })
      ] })
    }
  );
  return /* @__PURE__ */ l("div", { "data-full-bleed": !0, style: n.layout, children: [
    pe && m && /* @__PURE__ */ r(
      "div",
      {
        style: n.sidebarBackdrop,
        onClick: () => te(!1)
      }
    ),
    pe && /* @__PURE__ */ l("div", { style: { ...n.sidebar, ...m ? n.sidebarMobile : null }, children: [
      /* @__PURE__ */ l("div", { style: n.sidebarHeader, children: [
        /* @__PURE__ */ r("span", { style: n.sidebarTitle, children: e("playground.conversations") }),
        /* @__PURE__ */ r(
          "button",
          {
            style: n.newBtn,
            onClick: Ee,
            title: e("playground.new_conversation"),
            children: /* @__PURE__ */ r("svg", { width: "14", height: "14", viewBox: "0 0 14 14", fill: "none", stroke: "currentColor", strokeWidth: "1.8", strokeLinecap: "round", children: /* @__PURE__ */ r("path", { d: "M7 1v12M1 7h12" }) })
          }
        )
      ] }),
      /* @__PURE__ */ l("div", { style: n.convList, children: [
        o.map((t) => {
          const d = t.id === a;
          return /* @__PURE__ */ l(
            "div",
            {
              style: {
                ...n.convItem,
                background: d ? i("primarySubtle") : "transparent",
                borderColor: d ? i("borderFocus") : "transparent"
              },
              onClick: () => pt(t.id),
              children: [
                /* @__PURE__ */ r("svg", { width: "14", height: "14", viewBox: "0 0 24 24", fill: "none", stroke: i(d ? "primary" : "textTertiary"), strokeWidth: "1.5", strokeLinecap: "round", strokeLinejoin: "round", style: { flexShrink: 0, marginTop: 2 }, children: /* @__PURE__ */ r("path", { d: "M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" }) }),
                /* @__PURE__ */ r("span", { style: {
                  ...n.convTitle,
                  color: i(d ? "text" : "textSecondary")
                }, children: t.title || e("playground.new_conversation") }),
                /* @__PURE__ */ r(
                  "button",
                  {
                    style: n.deleteBtn,
                    onClick: (g) => {
                      g.stopPropagation(), rt(t.id);
                    },
                    title: e("playground.delete_conversation"),
                    children: /* @__PURE__ */ r("svg", { width: "12", height: "12", viewBox: "0 0 12 12", fill: "none", stroke: "currentColor", strokeWidth: "1.5", strokeLinecap: "round", children: /* @__PURE__ */ r("path", { d: "M2 2l8 8M10 2l-8 8" }) })
                  }
                )
              ]
            },
            t.id
          );
        }),
        o.length === 0 && /* @__PURE__ */ l("div", { style: n.emptyConvList, children: [
          /* @__PURE__ */ r("svg", { width: "20", height: "20", viewBox: "0 0 24 24", fill: "none", stroke: i("textTertiary"), strokeWidth: "1.5", strokeLinecap: "round", strokeLinejoin: "round", style: { opacity: 0.5 }, children: /* @__PURE__ */ r("path", { d: "M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" }) }),
          /* @__PURE__ */ r("span", { children: e("playground.no_conversations") })
        ] })
      ] }),
      h && /* @__PURE__ */ l("div", { style: n.balanceBar, children: [
        /* @__PURE__ */ r("span", { style: n.balanceLabel, children: e("playground.balance") }),
        /* @__PURE__ */ l("span", { style: n.balanceValue, children: [
          "$",
          h.balance.toFixed(4)
        ] })
      ] })
    ] }),
    /* @__PURE__ */ l("div", { style: n.main, children: [
      /* @__PURE__ */ l("div", { style: { ...n.topBar, ...m ? n.topBarMobile : null }, children: [
        /* @__PURE__ */ l("div", { style: { ...n.topBarLeft, ...m ? n.topBarLeftMobile : null }, children: [
          /* @__PURE__ */ r("button", { style: n.toggleBtn, onClick: () => te(!pe), children: /* @__PURE__ */ r("svg", { width: "16", height: "16", viewBox: "0 0 16 16", fill: "none", stroke: "currentColor", strokeWidth: "1.5", strokeLinecap: "round", children: pe ? /* @__PURE__ */ l(fe, { children: [
            /* @__PURE__ */ r("path", { d: "M6 2v12" }),
            /* @__PURE__ */ r("path", { d: "M2 2h12v12H2z" }),
            /* @__PURE__ */ r("path", { d: "M10 6l-2 2 2 2" })
          ] }) : /* @__PURE__ */ l(fe, { children: [
            /* @__PURE__ */ r("path", { d: "M6 2v12" }),
            /* @__PURE__ */ r("path", { d: "M2 2h12v12H2z" }),
            /* @__PURE__ */ r("path", { d: "M8 6l2 2-2 2" })
          ] }) }) }),
          /* @__PURE__ */ l("div", { style: { ...n.selectors, ...m ? n.selectorsMobile : null }, children: [
            /* @__PURE__ */ l("div", { style: { ...n.selectorGroup, ...m ? n.selectorGroupMobile : null }, children: [
              /* @__PURE__ */ r("label", { style: n.selectorLabel, children: "API Key" }),
              /* @__PURE__ */ l(
                "select",
                {
                  style: { ...n.select, ...m ? n.selectMobile : null },
                  value: z ?? (h == null ? void 0 : h.api_key_id) ?? "",
                  onChange: (t) => {
                    const d = Number(t.target.value || 0);
                    Le(d || null), Z("");
                  },
                  children: [
                    /* @__PURE__ */ r("option", { value: "", children: "Select key" }),
                    (h == null ? void 0 : h.api_key_id) && !de.some((t) => t.id === h.api_key_id) && /* @__PURE__ */ r("option", { value: h.api_key_id, children: h.api_key_name || "Current key" }),
                    de.map((t) => /* @__PURE__ */ r("option", { value: t.id, children: t.name }, t.id))
                  ]
                }
              )
            ] }),
            !m && /* @__PURE__ */ r("div", { style: n.selectorDivider }),
            /* @__PURE__ */ l("div", { style: { ...n.selectorGroup, ...m ? n.selectorGroupMobile : null }, children: [
              /* @__PURE__ */ r("label", { style: n.selectorLabel, children: e("playground.model") }),
              /* @__PURE__ */ r(
                "select",
                {
                  style: { ...n.select, ...m ? n.selectMobile : null },
                  value: M,
                  onChange: (t) => W(t.target.value),
                  children: I.map((t) => /* @__PURE__ */ l("option", { value: t.id, children: [
                    t.name || t.id,
                    Be(t) ? " · image" : Ge(t) ? " · reasoning" : ""
                  ] }, t.id))
                }
              )
            ] }),
            Se && /* @__PURE__ */ l(fe, { children: [
              !m && /* @__PURE__ */ r("div", { style: n.selectorDivider }),
              /* @__PURE__ */ l("div", { style: { ...n.selectorGroup, ...m ? n.selectorGroupMobile : null }, children: [
                /* @__PURE__ */ r("label", { style: n.selectorLabel, children: "Size" }),
                /* @__PURE__ */ r(
                  "select",
                  {
                    style: { ...n.select, ...m ? n.selectMobile : null },
                    value: ve,
                    onChange: (t) => Xe(t.target.value),
                    children: _t.map((t) => /* @__PURE__ */ r("option", { value: t.value, children: t.label }, t.value))
                  }
                )
              ] })
            ] }),
            Me && /* @__PURE__ */ l(fe, { children: [
              !m && /* @__PURE__ */ r("div", { style: n.selectorDivider }),
              /* @__PURE__ */ l("div", { style: { ...n.selectorGroup, ...m ? n.selectorGroupMobile : null }, children: [
                /* @__PURE__ */ r("label", { style: n.selectorLabel, children: "Effort" }),
                /* @__PURE__ */ l(
                  "select",
                  {
                    style: { ...n.select, ...m ? n.selectMobile : null },
                    value: be,
                    onChange: (t) => Ye(t.target.value),
                    children: [
                      /* @__PURE__ */ r("option", { value: "minimal", children: "Minimal" }),
                      /* @__PURE__ */ r("option", { value: "low", children: "Low" }),
                      /* @__PURE__ */ r("option", { value: "medium", children: "Medium" }),
                      /* @__PURE__ */ r("option", { value: "high", children: "High" }),
                      /* @__PURE__ */ r("option", { value: "xhigh", children: "XHigh" })
                    ]
                  }
                )
              ] })
            ] })
          ] })
        ] }),
        He && /* @__PURE__ */ r("span", { style: { ...n.topBarTitle, ...m ? n.topBarTitleMobile : null }, children: He.title || e("playground.new_conversation") })
      ] }),
      /* @__PURE__ */ l("div", { style: n.messagesArea, children: [
        !a && /* @__PURE__ */ l("div", { style: { ...n.emptyState, ...m ? n.emptyStateMobile : null }, children: [
          /* @__PURE__ */ r("div", { style: n.emptyIcon, children: /* @__PURE__ */ l("svg", { width: "48", height: "48", viewBox: "0 0 48 48", fill: "none", children: [
            /* @__PURE__ */ r("rect", { x: "4", y: "4", width: "40", height: "40", rx: "20", fill: i("primarySubtle") }),
            /* @__PURE__ */ r("path", { d: "M24 16v6m0 0v6m0-6h6m-6 0h-6", stroke: i("primary"), strokeWidth: "2", strokeLinecap: "round" })
          ] }) }),
          /* @__PURE__ */ r("div", { style: n.emptyTitle, children: e("playground.empty_title") }),
          /* @__PURE__ */ r("div", { style: n.emptyDesc, children: e("playground.empty_description") }),
          /* @__PURE__ */ l("button", { style: n.emptyBtn, onClick: Ee, children: [
            /* @__PURE__ */ r("svg", { width: "14", height: "14", viewBox: "0 0 14 14", fill: "none", stroke: "currentColor", strokeWidth: "2", strokeLinecap: "round", children: /* @__PURE__ */ r("path", { d: "M7 1v12M1 7h12" }) }),
            e("playground.new_conversation")
          ] })
        ] }),
        a && c.map((t) => /* @__PURE__ */ l("div", { style: { ...n.messageRow, ...m ? n.messageRowMobile : null }, children: [
          /* @__PURE__ */ r("div", { style: t.role === "user" ? n.avatarUser : n.avatarAssistant, children: t.role === "user" ? /* @__PURE__ */ l("svg", { width: "14", height: "14", viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: "2", strokeLinecap: "round", strokeLinejoin: "round", children: [
            /* @__PURE__ */ r("path", { d: "M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" }),
            /* @__PURE__ */ r("circle", { cx: "12", cy: "7", r: "4" })
          ] }) : /* @__PURE__ */ l("svg", { width: "14", height: "14", viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: "2", strokeLinecap: "round", strokeLinejoin: "round", children: [
            /* @__PURE__ */ r("path", { d: "M12 2a4 4 0 0 1 4 4v2a4 4 0 0 1-8 0V6a4 4 0 0 1 4-4z" }),
            /* @__PURE__ */ r("path", { d: "M6 12h12l2 8H4l2-8z" })
          ] }) }),
          /* @__PURE__ */ l("div", { style: n.messageBody, children: [
            /* @__PURE__ */ l("div", { style: n.messageHeader, children: [
              /* @__PURE__ */ r("div", { style: n.messageRole, children: t.role === "user" ? e("playground.you") : e("playground.assistant") }),
              ie(Pe(t.content))
            ] }),
            t.role === "assistant" && t.reasoning && /* @__PURE__ */ l("details", { style: n.reasoningBox, open: !0, children: [
              /* @__PURE__ */ l("summary", { style: n.reasoningSummary, children: [
                /* @__PURE__ */ r("span", { children: "Thinking" }),
                ie(t.reasoning, "Copy thinking", !0)
              ] }),
              /* @__PURE__ */ r("div", { style: n.reasoningContent, children: se(t.reasoning, re) })
            ] }),
            /* @__PURE__ */ r("div", { style: n.messageContent, children: se(t.content, re) }),
            t.role === "assistant" && t.model && /* @__PURE__ */ r("div", { style: n.messageMeta, children: /* @__PURE__ */ r("span", { style: n.metaBadge, children: t.model }) })
          ] })
        ] }, t.id)),
        V && v && /* @__PURE__ */ l("div", { style: { ...n.messageRow, ...m ? n.messageRowMobile : null }, children: [
          /* @__PURE__ */ r("div", { style: n.avatarAssistant, children: /* @__PURE__ */ l("svg", { width: "14", height: "14", viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: "2", strokeLinecap: "round", strokeLinejoin: "round", children: [
            /* @__PURE__ */ r("path", { d: "M12 2a4 4 0 0 1 4 4v2a4 4 0 0 1-8 0V6a4 4 0 0 1 4-4z" }),
            /* @__PURE__ */ r("path", { d: "M6 12h12l2 8H4l2-8z" })
          ] }) }),
          /* @__PURE__ */ l("div", { style: n.messageBody, children: [
            /* @__PURE__ */ l("div", { style: n.messageHeader, children: [
              /* @__PURE__ */ r("div", { style: n.messageRole, children: e("playground.assistant") }),
              ie(Pe(v))
            ] }),
            k && /* @__PURE__ */ l("details", { style: n.reasoningBox, open: !0, children: [
              /* @__PURE__ */ l("summary", { style: n.reasoningSummary, children: [
                /* @__PURE__ */ r("span", { children: "Thinking" }),
                ie(k, "Copy thinking", !0)
              ] }),
              /* @__PURE__ */ r("div", { style: n.reasoningContent, children: se(k, re) })
            ] }),
            /* @__PURE__ */ r("div", { style: n.messageContent, children: se(v, re) }),
            /* @__PURE__ */ l("div", { style: n.messageMeta, children: [
              /* @__PURE__ */ r("span", { style: n.streamingDot }),
              /* @__PURE__ */ r("span", { children: e("playground.streaming") })
            ] })
          ] })
        ] }),
        V && !v && /* @__PURE__ */ l("div", { style: { ...n.messageRow, ...m ? n.messageRowMobile : null }, children: [
          /* @__PURE__ */ r("div", { style: n.avatarAssistant, children: /* @__PURE__ */ l("svg", { width: "14", height: "14", viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: "2", strokeLinecap: "round", strokeLinejoin: "round", children: [
            /* @__PURE__ */ r("path", { d: "M12 2a4 4 0 0 1 4 4v2a4 4 0 0 1-8 0V6a4 4 0 0 1 4-4z" }),
            /* @__PURE__ */ r("path", { d: "M6 12h12l2 8H4l2-8z" })
          ] }) }),
          /* @__PURE__ */ l("div", { style: n.messageBody, children: [
            /* @__PURE__ */ r("div", { style: n.messageHeader, children: /* @__PURE__ */ r("div", { style: n.messageRole, children: e("playground.assistant") }) }),
            k ? /* @__PURE__ */ l("details", { style: n.reasoningBox, open: !0, children: [
              /* @__PURE__ */ l("summary", { style: n.reasoningSummary, children: [
                /* @__PURE__ */ r("span", { children: "Thinking" }),
                ie(k, "Copy thinking", !0)
              ] }),
              /* @__PURE__ */ r("div", { style: n.reasoningContent, children: se(k, re) })
            ] }) : /* @__PURE__ */ r("div", { style: { ...n.messageContent, opacity: 0.5 }, children: /* @__PURE__ */ r("span", { style: n.thinkingDots, children: e("playground.thinking") }) })
          ] })
        ] }),
        Re && /* @__PURE__ */ l("div", { style: { ...n.errorBar, ...m ? n.errorBarMobile : null }, children: [
          /* @__PURE__ */ l("svg", { width: "14", height: "14", viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: "2", strokeLinecap: "round", strokeLinejoin: "round", children: [
            /* @__PURE__ */ r("circle", { cx: "12", cy: "12", r: "10" }),
            /* @__PURE__ */ r("path", { d: "M12 8v4m0 4h.01" })
          ] }),
          Re
        ] }),
        ce && /* @__PURE__ */ r("div", { style: n.interactionNotice, children: ce }),
        /* @__PURE__ */ r("div", { ref: $e })
      ] }),
      a && /* @__PURE__ */ r("div", { style: { ...n.inputArea, ...m ? n.inputAreaMobile : null }, children: /* @__PURE__ */ l("div", { style: { ...n.inputWrapper, ...V ? n.inputWrapperStreaming : null }, children: [
        L.length > 0 && /* @__PURE__ */ r("div", { style: n.imagePreviewList, children: L.map((t) => /* @__PURE__ */ l("div", { style: n.imagePreviewItem, children: [
          /* @__PURE__ */ r("img", { src: t.url, alt: t.name, style: n.imagePreview }),
          /* @__PURE__ */ r(
            "button",
            {
              type: "button",
              style: n.removeImageBtn,
              onClick: () => at(t.id),
              "aria-label": `Remove ${t.name}`,
              children: "×"
            }
          )
        ] }, t.id)) }),
        /* @__PURE__ */ r(
          "textarea",
          {
            ref: we,
            style: n.textarea,
            value: $,
            onChange: (t) => {
              f(t.target.value), ct(t.target);
            },
            onPaste: ot,
            onKeyDown: lt,
            placeholder: e("playground.input_placeholder"),
            rows: 1,
            disabled: V
          }
        ),
        /* @__PURE__ */ r(
          "input",
          {
            ref: ue,
            type: "file",
            accept: "image/*",
            multiple: !0,
            style: n.fileInput,
            onChange: it,
            disabled: V
          }
        ),
        /* @__PURE__ */ l("div", { style: { ...n.inputActions, ...m ? n.inputActionsMobile : null }, children: [
          /* @__PURE__ */ r("span", { style: { ...n.inputHint, ...m ? n.inputHintMobile : null }, children: e("playground.input_hint") }),
          /* @__PURE__ */ l("div", { style: { ...n.inputButtonGroup, ...m ? n.inputButtonGroupMobile : null }, children: [
            /* @__PURE__ */ l(
              "button",
              {
                type: "button",
                style: { ...n.attachBtn, ...m ? n.actionBtnMobile : null },
                onClick: dt,
                disabled: V,
                title: "Attach images",
                children: [
                  /* @__PURE__ */ l("svg", { width: "14", height: "14", viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: "2", strokeLinecap: "round", strokeLinejoin: "round", children: [
                    /* @__PURE__ */ r("rect", { x: "3", y: "3", width: "18", height: "18", rx: "2" }),
                    /* @__PURE__ */ r("circle", { cx: "8.5", cy: "8.5", r: "1.5" }),
                    /* @__PURE__ */ r("path", { d: "M21 15l-5-5L5 21" })
                  ] }),
                  "Image"
                ]
              }
            ),
            V ? /* @__PURE__ */ l("button", { style: { ...n.stopBtn, ...m ? n.actionBtnMobile : null }, onClick: st, children: [
              /* @__PURE__ */ r("svg", { width: "12", height: "12", viewBox: "0 0 12 12", fill: "currentColor", children: /* @__PURE__ */ r("rect", { x: "2", y: "2", width: "8", height: "8", rx: "1" }) }),
              e("playground.stop")
            ] }) : /* @__PURE__ */ l(
              "button",
              {
                style: {
                  ...n.sendBtn,
                  ...m ? n.actionBtnMobile : null,
                  opacity: We ? 1 : 0.4
                },
                onClick: Ie,
                disabled: !We,
                title: he ? void 0 : "Select an API key first",
                children: [
                  /* @__PURE__ */ l("svg", { width: "14", height: "14", viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: "2", strokeLinecap: "round", strokeLinejoin: "round", children: [
                    /* @__PURE__ */ r("path", { d: "M22 2L11 13" }),
                    /* @__PURE__ */ r("path", { d: "M22 2l-7 20-4-9-9-4 20-7z" })
                  ] }),
                  e("playground.send")
                ]
              }
            )
          ] })
        ] })
      ] }) })
    ] }),
    /* @__PURE__ */ r("style", { children: Ft })
  ] });
}
const Ft = `
@keyframes pg-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.3; }
}
@keyframes pg-fadein {
  from { opacity: 0; transform: translateY(6px); }
  to { opacity: 1; transform: translateY(0); }
}
`, n = {
  layout: {
    display: "flex",
    height: "100%",
    minHeight: 0,
    minWidth: 0,
    position: "relative",
    isolation: "isolate",
    background: i("bgDeep"),
    fontFamily: i("fontSans"),
    color: i("text"),
    overflow: "hidden"
  },
  // ── Sidebar ──
  sidebar: {
    width: 280,
    minWidth: 280,
    display: "flex",
    flexDirection: "column",
    minHeight: 0,
    background: i("bg"),
    borderRight: `1px solid ${i("borderSubtle")}`,
    position: "relative",
    zIndex: 3
  },
  sidebarBackdrop: {
    position: "absolute",
    inset: 0,
    background: "rgba(6, 10, 18, 0.64)",
    zIndex: 2
  },
  sidebarMobile: {
    position: "absolute",
    top: 0,
    bottom: 0,
    left: 0,
    width: "min(84vw, 320px)",
    minWidth: "min(84vw, 320px)",
    boxShadow: "0 18px 48px rgba(0, 0, 0, 0.32)"
  },
  sidebarHeader: {
    display: "flex",
    justifyContent: "space-between",
    alignItems: "center",
    padding: "18px 16px 14px"
  },
  sidebarTitle: {
    fontSize: 11,
    fontWeight: 600,
    textTransform: "uppercase",
    letterSpacing: "0.08em",
    color: i("textTertiary")
  },
  newBtn: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    width: 28,
    height: 28,
    border: `1px solid ${i("border")}`,
    borderRadius: i("radiusSm"),
    background: i("bgSurface"),
    color: i("textSecondary"),
    cursor: "pointer",
    transition: i("transition")
  },
  convList: {
    flex: 1,
    overflowY: "auto",
    padding: "4px 8px"
  },
  convItem: {
    display: "flex",
    alignItems: "flex-start",
    gap: 8,
    padding: "10px 10px",
    borderRadius: i("radiusSm"),
    cursor: "pointer",
    transition: i("transition"),
    border: "1px solid transparent",
    marginBottom: 2
  },
  convTitle: {
    flex: 1,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    fontSize: 13,
    lineHeight: "18px"
  },
  deleteBtn: {
    background: "none",
    border: "none",
    color: i("textTertiary"),
    cursor: "pointer",
    padding: "2px",
    lineHeight: 1,
    flexShrink: 0,
    opacity: 0.5,
    transition: i("transition"),
    marginTop: 1
  },
  emptyConvList: {
    display: "flex",
    flexDirection: "column",
    alignItems: "center",
    gap: 8,
    padding: "32px 16px",
    color: i("textTertiary"),
    fontSize: 12
  },
  balanceBar: {
    display: "flex",
    justifyContent: "space-between",
    alignItems: "center",
    padding: "12px 16px",
    borderTop: `1px solid ${i("borderSubtle")}`
  },
  balanceLabel: {
    fontSize: 11,
    fontWeight: 500,
    textTransform: "uppercase",
    letterSpacing: "0.06em",
    color: i("textTertiary")
  },
  balanceValue: {
    fontSize: 13,
    fontWeight: 600,
    fontFamily: i("fontMono"),
    color: i("primary")
  },
  // ── Main ──
  main: {
    flex: 1,
    display: "flex",
    flexDirection: "column",
    minHeight: 0,
    minWidth: 0,
    overflow: "hidden"
  },
  // ── Top bar ──
  topBar: {
    display: "flex",
    alignItems: "center",
    justifyContent: "space-between",
    gap: 16,
    padding: "8px 20px",
    borderBottom: `1px solid ${i("borderSubtle")}`,
    background: i("bg"),
    flexShrink: 0,
    minHeight: 52
  },
  topBarMobile: {
    alignItems: "flex-start",
    flexDirection: "column",
    padding: "8px 12px",
    gap: 8
  },
  topBarLeft: {
    display: "flex",
    alignItems: "center",
    gap: 12
  },
  topBarLeftMobile: {
    width: "100%",
    alignItems: "flex-start",
    gap: 8
  },
  toggleBtn: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    width: 32,
    height: 32,
    border: "none",
    borderRadius: i("radiusSm"),
    background: "transparent",
    color: i("textSecondary"),
    cursor: "pointer",
    transition: i("transition"),
    flexShrink: 0
  },
  selectors: {
    display: "flex",
    alignItems: "center",
    gap: 0,
    background: i("bgSurface"),
    borderRadius: i("radiusSm"),
    border: `1px solid ${i("borderSubtle")}`,
    overflow: "hidden"
  },
  selectorsMobile: {
    flex: 1,
    minWidth: 0,
    display: "grid",
    gridTemplateColumns: "repeat(2, minmax(0, 1fr))",
    alignItems: "stretch",
    gap: 6,
    padding: 6,
    overflow: "visible"
  },
  selectorGroup: {
    display: "flex",
    alignItems: "center",
    gap: 6,
    padding: "6px 12px",
    minWidth: 0
  },
  selectorGroupMobile: {
    minWidth: 0,
    padding: 0,
    flexDirection: "column",
    alignItems: "stretch",
    gap: 3
  },
  selectorLabel: {
    fontSize: 10,
    fontWeight: 600,
    textTransform: "uppercase",
    letterSpacing: "0.08em",
    color: i("textTertiary"),
    whiteSpace: "nowrap"
  },
  selectorDivider: {
    width: 1,
    height: 24,
    background: i("borderSubtle"),
    flexShrink: 0
  },
  select: {
    padding: "2px 4px",
    border: "none",
    background: "transparent",
    color: i("text"),
    fontSize: 13,
    fontWeight: 500,
    outline: "none",
    cursor: "pointer",
    fontFamily: i("fontSans"),
    minWidth: 0
  },
  selectMobile: {
    width: "100%",
    minHeight: 30,
    borderRadius: i("radiusSm"),
    padding: "5px 7px",
    background: i("bgDeep"),
    fontSize: 12
  },
  topBarTitle: {
    fontSize: 12,
    color: i("textTertiary"),
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap"
  },
  topBarTitleMobile: {
    width: "100%",
    display: "none"
  },
  // ── Messages ──
  messagesArea: {
    flex: 1,
    minHeight: 0,
    overflowY: "auto",
    display: "flex",
    flexDirection: "column",
    position: "relative"
  },
  // ── Empty state ──
  emptyState: {
    display: "flex",
    flexDirection: "column",
    alignItems: "center",
    justifyContent: "center",
    flex: 1,
    gap: 12,
    padding: 40,
    animation: "pg-fadein 0.4s ease-out"
  },
  emptyStateMobile: {
    padding: "32px 20px"
  },
  emptyIcon: {
    marginBottom: 8
  },
  emptyTitle: {
    fontSize: 18,
    fontWeight: 600,
    color: i("text"),
    letterSpacing: "-0.02em"
  },
  emptyDesc: {
    fontSize: 13,
    color: i("textSecondary"),
    maxWidth: 300,
    textAlign: "center",
    lineHeight: 1.5
  },
  emptyBtn: {
    display: "inline-flex",
    alignItems: "center",
    gap: 8,
    padding: "10px 22px",
    border: "none",
    borderRadius: i("radiusMd"),
    background: i("primary"),
    color: i("textInverse"),
    fontSize: 14,
    fontWeight: 600,
    cursor: "pointer",
    transition: i("transition"),
    marginTop: 8,
    fontFamily: i("fontSans")
  },
  // ── Message row ──
  messageRow: {
    display: "flex",
    gap: 14,
    padding: "20px 28px",
    animation: "pg-fadein 0.25s ease-out",
    borderBottom: `1px solid ${i("borderSubtle")}`
  },
  messageRowMobile: {
    gap: 10,
    padding: "16px 14px"
  },
  avatarUser: {
    width: 30,
    height: 30,
    borderRadius: "50%",
    background: i("primary"),
    color: i("textInverse"),
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    flexShrink: 0
  },
  avatarAssistant: {
    width: 30,
    height: 30,
    borderRadius: "50%",
    background: i("bgSurface"),
    border: `1px solid ${i("border")}`,
    color: i("textSecondary"),
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    flexShrink: 0
  },
  messageBody: {
    flex: 1,
    minWidth: 0
  },
  messageHeader: {
    display: "flex",
    alignItems: "center",
    gap: 8,
    minHeight: 24,
    marginBottom: 4
  },
  messageRole: {
    fontSize: 12,
    fontWeight: 600,
    color: i("text")
  },
  messageCopyBtn: {
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    width: 24,
    height: 24,
    border: `1px solid ${i("borderSubtle")}`,
    borderRadius: "999px",
    background: "transparent",
    color: i("textTertiary"),
    cursor: "pointer",
    transition: i("transition")
  },
  messageContent: {
    fontSize: 14,
    lineHeight: 1.72,
    wordBreak: "break-word",
    color: "#cfd6e6"
  },
  markdownParagraph: {
    margin: "0 0 11px"
  },
  markdownH1: {
    margin: "2px 0 14px",
    fontSize: 21,
    lineHeight: 1.25,
    color: "#eef4ff",
    letterSpacing: "-0.02em"
  },
  markdownH2: {
    margin: "18px 0 10px",
    fontSize: 17,
    lineHeight: 1.3,
    color: "#e8eefb",
    letterSpacing: "-0.01em"
  },
  markdownH3: {
    margin: "16px 0 8px",
    fontSize: 15,
    lineHeight: 1.35,
    color: "#dfe7f6"
  },
  markdownH4: {
    margin: "14px 0 8px",
    fontSize: 14,
    lineHeight: 1.4,
    color: "#d8e0ef"
  },
  markdownList: {
    margin: "0 0 12px",
    paddingLeft: 20,
    color: "#cfd6e6"
  },
  markdownListItem: {
    margin: "4px 0"
  },
  markdownBlockquote: {
    margin: "0 0 12px",
    padding: "9px 13px",
    borderLeft: "3px solid rgba(62, 207, 180, 0.48)",
    borderRadius: "0 10px 10px 0",
    background: "rgba(62, 207, 180, 0.055)",
    color: "#aeb8ca"
  },
  markdownCodeBlock: {
    margin: "4px 0 14px",
    padding: "13px 15px",
    borderRadius: i("radiusSm"),
    background: "linear-gradient(180deg, rgba(17, 23, 36, 0.92), rgba(10, 14, 24, 0.92))",
    border: "1px solid rgba(148, 175, 225, 0.075)",
    color: "#d5deef",
    fontFamily: i("fontMono"),
    fontSize: 12.5,
    lineHeight: 1.72,
    overflowX: "auto",
    whiteSpace: "pre",
    boxShadow: "inset 0 1px 0 rgba(255, 255, 255, 0.025)"
  },
  markdownInlineCode: {
    padding: "1px 5px 2px",
    borderRadius: 6,
    background: "rgba(125, 211, 252, 0.08)",
    border: "1px solid rgba(125, 211, 252, 0.11)",
    color: "#b8e7ff",
    fontFamily: i("fontMono"),
    fontSize: "0.9em"
  },
  markdownLink: {
    color: "#6ee7d1",
    textDecoration: "underline",
    textDecorationColor: "rgba(110, 231, 209, 0.28)",
    textUnderlineOffset: 3
  },
  markdownDivider: {
    height: 1,
    border: 0,
    background: "linear-gradient(90deg, transparent, rgba(148, 175, 225, 0.14), transparent)",
    margin: "16px 0"
  },
  reasoningBox: {
    marginBottom: 10,
    padding: "10px 12px",
    borderRadius: i("radiusSm"),
    background: i("bgSurface"),
    border: `1px solid ${i("borderSubtle")}`
  },
  reasoningSummary: {
    cursor: "pointer",
    display: "flex",
    alignItems: "center",
    gap: 8,
    fontSize: 12,
    fontWeight: 600,
    color: i("textSecondary"),
    userSelect: "none"
  },
  reasoningContent: {
    marginTop: 8,
    fontSize: 13,
    lineHeight: 1.6,
    wordBreak: "break-word",
    color: "#9ea9bd"
  },
  imageGroup: {
    display: "flex",
    flexWrap: "wrap",
    alignItems: "flex-start",
    gap: 16,
    margin: "10px 0 12px"
  },
  generatedImageFrame: {
    display: "inline-flex",
    flexDirection: "column",
    alignItems: "flex-start",
    gap: 8,
    flex: "1 1 260px",
    maxWidth: "min(100%, 420px)"
  },
  generatedImage: {
    display: "block",
    maxHeight: 420,
    width: "100%",
    height: "auto",
    borderRadius: i("radiusMd"),
    border: `1px solid ${i("borderSubtle")}`,
    objectFit: "contain"
  },
  imageDownloadBtn: {
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    gap: 6,
    minHeight: 30,
    padding: "5px 10px",
    borderRadius: "999px",
    border: `1px solid ${i("borderSubtle")}`,
    background: i("bgSurface"),
    color: i("textSecondary"),
    fontFamily: i("fontSans"),
    fontSize: 12,
    fontWeight: 500,
    cursor: "pointer",
    transition: i("transition")
  },
  interactionNotice: {
    position: "sticky",
    bottom: 12,
    alignSelf: "center",
    zIndex: 4,
    padding: "7px 12px",
    borderRadius: "999px",
    background: "rgba(10, 14, 24, 0.9)",
    border: `1px solid ${i("borderSubtle")}`,
    color: i("textSecondary"),
    fontSize: 12,
    boxShadow: "0 10px 28px rgba(0, 0, 0, 0.22)"
  },
  messageMeta: {
    display: "flex",
    alignItems: "center",
    gap: 6,
    marginTop: 8,
    fontSize: 11,
    color: i("textTertiary")
  },
  metaBadge: {
    display: "inline-flex",
    padding: "2px 8px",
    borderRadius: "999px",
    background: i("bgSurface"),
    border: `1px solid ${i("borderSubtle")}`,
    fontSize: 11,
    fontFamily: i("fontMono"),
    color: i("textSecondary")
  },
  streamingDot: {
    width: 6,
    height: 6,
    borderRadius: "50%",
    background: i("primary"),
    animation: "pg-pulse 1.2s ease-in-out infinite"
  },
  thinkingDots: {
    animation: "pg-pulse 1.5s ease-in-out infinite"
  },
  // ── Error ──
  errorBar: {
    display: "flex",
    alignItems: "center",
    gap: 8,
    margin: "8px 28px",
    padding: "10px 14px",
    borderRadius: i("radiusSm"),
    background: i("dangerSubtle"),
    color: i("danger"),
    fontSize: 13,
    border: `1px solid ${i("danger")}`,
    borderColor: "rgba(251, 113, 133, 0.2)"
  },
  errorBarMobile: {
    margin: "8px 14px"
  },
  // ── Input ──
  inputArea: {
    padding: "16px 28px 20px",
    borderTop: `1px solid ${i("borderSubtle")}`,
    background: i("bg"),
    flexShrink: 0
  },
  inputAreaMobile: {
    padding: "10px 12px 12px"
  },
  inputWrapper: {
    display: "flex",
    flexDirection: "column",
    gap: 8,
    border: `1px solid ${i("border")}`,
    borderRadius: i("radiusMd"),
    background: i("bgSurface"),
    padding: "10px 12px 8px",
    transition: i("transition")
  },
  inputWrapperStreaming: {
    paddingTop: 8,
    paddingBottom: 8
  },
  imagePreviewList: {
    display: "flex",
    flexWrap: "wrap",
    gap: 8
  },
  imagePreviewItem: {
    position: "relative",
    width: 76,
    height: 76,
    borderRadius: i("radiusSm"),
    overflow: "hidden",
    border: `1px solid ${i("borderSubtle")}`,
    background: i("bgHover")
  },
  imagePreview: {
    width: "100%",
    height: "100%",
    objectFit: "cover",
    display: "block"
  },
  removeImageBtn: {
    position: "absolute",
    top: 4,
    right: 4,
    width: 20,
    height: 20,
    border: "none",
    borderRadius: 999,
    background: "rgba(0, 0, 0, 0.62)",
    color: "#fff",
    cursor: "pointer",
    lineHeight: "20px",
    padding: 0,
    fontSize: 16
  },
  textarea: {
    width: "100%",
    border: "none",
    background: "transparent",
    color: i("text"),
    fontSize: 14,
    fontFamily: i("fontSans"),
    resize: "none",
    outline: "none",
    lineHeight: 1.55,
    height: 24,
    minHeight: 24,
    maxHeight: 128
  },
  inputActions: {
    display: "flex",
    justifyContent: "space-between",
    alignItems: "center",
    gap: 12
  },
  inputActionsMobile: {
    alignItems: "center",
    gap: 8
  },
  inputButtonGroup: {
    display: "flex",
    alignItems: "center",
    gap: 8
  },
  inputButtonGroupMobile: {
    flex: 1,
    minWidth: 0,
    justifyContent: "flex-end"
  },
  fileInput: {
    display: "none"
  },
  inputHint: {
    fontSize: 11,
    color: i("textTertiary")
  },
  inputHintMobile: {
    display: "none"
  },
  attachBtn: {
    display: "inline-flex",
    alignItems: "center",
    gap: 6,
    padding: "6px 12px",
    border: `1px solid ${i("border")}`,
    borderRadius: i("radiusSm"),
    background: i("bgSurface"),
    color: i("textSecondary"),
    fontSize: 13,
    fontWeight: 600,
    cursor: "pointer",
    transition: i("transition"),
    fontFamily: i("fontSans")
  },
  sendBtn: {
    display: "inline-flex",
    alignItems: "center",
    gap: 6,
    padding: "6px 16px",
    border: "none",
    borderRadius: i("radiusSm"),
    background: i("primary"),
    color: i("textInverse"),
    fontSize: 13,
    fontWeight: 600,
    cursor: "pointer",
    transition: i("transition"),
    fontFamily: i("fontSans")
  },
  stopBtn: {
    display: "inline-flex",
    alignItems: "center",
    gap: 6,
    padding: "6px 16px",
    border: "none",
    borderRadius: i("radiusSm"),
    background: i("danger"),
    color: i("textInverse"),
    fontSize: 13,
    fontWeight: 600,
    cursor: "pointer",
    fontFamily: i("fontSans")
  },
  actionBtnMobile: {
    flex: 1,
    minWidth: 0,
    justifyContent: "center"
  }
}, Vt = {
  routes: [
    { path: "/playground", component: Ot }
  ]
};
export {
  Vt as default
};
