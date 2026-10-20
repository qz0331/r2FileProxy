/* R2 Proxy · 前端交互工具库（无第三方依赖） */
(function () {
  "use strict";

  const ICONS = {
    success: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>',
    error: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 8v5"/><path d="M12 16.5h.01"/></svg>',
    info: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 11v5"/><path d="M12 7.5h.01"/></svg>',
  };


  /* ---------------- Theme（日间 / 夜间 / 跟随系统） ---------------- */
  const THEME_KEY = "r2proxy-theme";
  const themeListeners = [];

  function systemTheme() {
    return window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }

  function storedTheme() {
    try { return localStorage.getItem(THEME_KEY) || "auto"; } catch (e) { return "auto"; }
  }

  function resolvedTheme(mode) {
    return mode === "auto" ? systemTheme() : mode;
  }

  function applyTheme(mode) {
    const m = mode || storedTheme();
    const resolved = resolvedTheme(m);
    const root = document.documentElement;
    // auto 时移除属性，交给 CSS 的 prefers-color-scheme 处理
    if (m === "auto") root.removeAttribute("data-theme");
    else root.setAttribute("data-theme", m);

    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.setAttribute("content", resolved === "dark" ? "#0b1020" : "#4338ca");

    themeListeners.forEach((fn) => {
      try { fn(m, resolved); } catch (e) { /* ignore */ }
    });
    return resolved;
  }

  function setTheme(mode) {
    const m = ["light", "dark", "auto"].includes(mode) ? mode : "auto";
    try { localStorage.setItem(THEME_KEY, m); } catch (e) { /* ignore */ }
    return applyTheme(m);
  }

  function toggleTheme() {
    // 在 浅色 → 深色 → 跟随系统 之间循环
    const current = storedTheme();
    const next = current === "light" ? "dark" : current === "dark" ? "auto" : "light";
    setTheme(next);
    return next;
  }

  function onThemeChange(fn) {
    themeListeners.push(fn);
    fn(storedTheme(), resolvedTheme(storedTheme()));
  }

  // 尽早应用，避免首屏闪烁
  applyTheme(storedTheme());

  if (window.matchMedia) {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const handler = () => { if (storedTheme() === "auto") applyTheme("auto"); };
    if (mq.addEventListener) mq.addEventListener("change", handler);
    else if (mq.addListener) mq.addListener(handler);
  }

  /* ---------------- Toast ---------------- */
  function ensureToastStack() {
    let stack = document.querySelector(".toast-stack");
    if (!stack) {
      stack = document.createElement("div");
      stack.className = "toast-stack";
      document.body.appendChild(stack);
    }
    return stack;
  }

  function toast(message, type, duration) {
    const stack = ensureToastStack();
    const el = document.createElement("div");
    el.className = "toast " + (type || "info");
    el.innerHTML = (ICONS[type] || ICONS.info) + "<div></div>";
    el.lastElementChild.textContent = String(message == null ? "" : message);
    stack.appendChild(el);

    const ttl = duration == null ? (type === "error" ? 4200 : 2600) : duration;
    const remove = () => {
      el.classList.add("leaving");
      setTimeout(() => el.remove(), 220);
    };
    el.addEventListener("click", remove);
    setTimeout(remove, ttl);
  }

  /* ---------------- HTTP ---------------- */
  async function request(url, options) {
    const opts = Object.assign({ headers: {} }, options || {});
    if (opts.body && typeof opts.body === "object" && !(opts.body instanceof FormData)) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(opts.body);
      opts.method = opts.method || "POST";
    }
    opts.headers.Accept = opts.headers.Accept || "application/json";

    let resp;
    try {
      resp = await fetch(url, opts);
    } catch (e) {
      throw new Error(navigator.onLine ? "网络异常，请稍后重试" : "设备已离线");
    }

    const text = await resp.text();
    let data = null;
    if (text) {
      try {
        data = JSON.parse(text);
      } catch (e) {
        data = { success: false, error: { message: text.slice(0, 200) } };
      }
    }

    if (!resp.ok || (data && data.success === false)) {
      const message = (data && data.error && data.error.message) || defaultMessage(resp.status);
      const err = new Error(message);
      err.status = resp.status;
      err.code = data && data.error && data.error.code;
      err.data = data;
      throw err;
    }
    return data;
  }

  function defaultMessage(status) {
    switch (status) {
      case 400: return "请求参数有误";
      case 401: return "登录已过期，请重新登录";
      case 403: return "没有权限执行此操作";
      case 404: return "资源不存在";
      case 413: return "文件过大";
      case 429: return "请求过于频繁，请稍后再试";
      case 500: return "服务器开小差了，请稍后再试";
      default: return "请求失败（HTTP " + status + "）";
    }
  }

  /* ---------------- Format ---------------- */
  function formatSize(bytes) {
    const n = Number(bytes) || 0;
    if (n <= 0) return "-";
    const units = ["B", "KB", "MB", "GB", "TB"];
    let i = 0;
    let v = n;
    while (v >= 1024 && i < units.length - 1) {
      v /= 1024;
      i++;
    }
    return (i === 0 ? v : v.toFixed(v >= 100 ? 0 : 1)) + " " + units[i];
  }

  function formatTime(value, withTime) {
    if (!value) return "-";
    const d = new Date(value);
    if (isNaN(d.getTime())) return String(value);
    const p = (n) => String(n).padStart(2, "0");
    const date = d.getFullYear() + "-" + p(d.getMonth() + 1) + "-" + p(d.getDate());
    if (withTime === false) return date;
    return date + " " + p(d.getHours()) + ":" + p(d.getMinutes());
  }

  function timeAgo(value) {
    if (!value) return "";
    const d = new Date(value);
    if (isNaN(d.getTime())) return "";
    const diff = (Date.now() - d.getTime()) / 1000;
    if (diff < 60) return "刚刚";
    if (diff < 3600) return Math.floor(diff / 60) + " 分钟前";
    if (diff < 86400) return Math.floor(diff / 3600) + " 小时前";
    if (diff < 86400 * 30) return Math.floor(diff / 86400) + " 天前";
    return formatTime(value, false);
  }

  function countdown(seconds) {
    if (seconds == null || seconds < 0) return "已过期";
    const s = Math.floor(seconds);
    if (s < 60) return s + " 秒";
    if (s < 3600) return Math.floor(s / 60) + " 分钟";
    if (s < 86400) {
      const h = Math.floor(s / 3600);
      const m = Math.floor((s % 3600) / 60);
      return m ? h + " 小时 " + m + " 分" : h + " 小时";
    }
    const d = Math.floor(s / 86400);
    const h = Math.floor((s % 86400) / 3600);
    return h ? d + " 天 " + h + " 小时" : d + " 天";
  }

  function escapeHTML(value) {
    return String(value == null ? "" : value).replace(/[&<>"']/g, (ch) => ({
      "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
    }[ch]));
  }

  /* ---------------- Clipboard ---------------- */
  async function copy(text) {
    const value = String(text == null ? "" : text);
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(value);
      } else {
        const ta = document.createElement("textarea");
        ta.value = value;
        ta.setAttribute("readonly", "");
        ta.style.position = "fixed";
        ta.style.top = "-1000px";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        ta.setSelectionRange(0, value.length);
        const ok = document.execCommand("copy");
        ta.remove();
        if (!ok) throw new Error("copy failed");
      }
      toast("已复制到剪贴板", "success");
      return true;
    } catch (e) {
      toast("复制失败，请手动选择复制", "error");
      return false;
    }
  }

  /* ---------------- Modal ---------------- */
  function openModal(id) {
    const el = typeof id === "string" ? document.getElementById(id) : id;
    if (!el) return;
    el.classList.add("open");
    document.body.style.overflow = "hidden";
    const focusable = el.querySelector("input, textarea, select, button");
    if (focusable) setTimeout(() => focusable.focus({ preventScroll: true }), 60);
  }

  function closeModal(id) {
    const el = typeof id === "string" ? document.getElementById(id) : id;
    if (!el) return;
    el.classList.remove("open");
    if (!document.querySelector(".modal.open")) document.body.style.overflow = "";
  }

  function bindModalDismiss() {
    document.querySelectorAll(".modal").forEach((modal) => {
      modal.addEventListener("click", (e) => {
        if (e.target === modal || e.target.closest("[data-close]")) {
          closeModal(modal);
        }
      });
    });
    document.addEventListener("keydown", (e) => {
      if (e.key !== "Escape") return;
      const open = document.querySelector(".modal.open");
      if (open) closeModal(open);
      const menu = document.querySelector(".mobile-menu.open");
      if (menu) menu.classList.remove("open");
    });
  }

  /* ---------------- Confirm ---------------- */
  function confirmDialog(opts) {
    const options = typeof opts === "string" ? { message: opts } : opts || {};
    return new Promise((resolve) => {
      const modal = document.createElement("div");
      modal.className = "modal open";
      modal.innerHTML =
        '<div class="modal-card" role="dialog" aria-modal="true">' +
        '<div class="modal-head"><div><div class="modal-title" id="cfm-title"></div></div></div>' +
        '<div class="modal-body" style="padding-top:10px"><p class="hint" style="font-size:13.5px" id="cfm-msg"></p></div>' +
        '<div class="modal-foot">' +
        '<button class="btn" data-no type="button">取消</button>' +
        '<button class="btn ' + (options.danger === false ? "btn-primary" : "btn-danger") + '" data-yes type="button"></button>' +
        "</div></div>";
      modal.querySelector("#cfm-title").textContent = options.title || "确认操作";
      modal.querySelector("#cfm-msg").textContent = options.message || "确定继续吗？";
      modal.querySelector("[data-yes]").textContent = options.confirmText || "确定";
      modal.querySelector("[data-no]").textContent = options.cancelText || "取消";
      document.body.appendChild(modal);
      document.body.style.overflow = "hidden";

      const finish = (result) => {
        modal.remove();
        if (!document.querySelector(".modal.open")) document.body.style.overflow = "";
        document.removeEventListener("keydown", onKey);
        resolve(result);
      };
      const onKey = (e) => {
        if (e.key === "Escape") finish(false);
        if (e.key === "Enter") finish(true);
      };

      modal.addEventListener("click", (e) => {
        if (e.target === modal || e.target.closest("[data-no]")) finish(false);
        if (e.target.closest("[data-yes]")) finish(true);
      });
      document.addEventListener("keydown", onKey);
      setTimeout(() => modal.querySelector("[data-yes]").focus(), 40);
    });
  }

  /* ---------------- QR Code ---------------- */
  // 依赖本地 qrcode.min.js（qrcode-generator），在 _components.html 的 scripts 模板中加载。
  function qrInto(container, text, size) {
    if (!container) return false;
    const px = size || 200;
    container.innerHTML = "";
    try {
      if (typeof window.qrcode !== "function") {
        throw new Error("二维码库未加载（/static/qrcode.min.js）");
      }
      const qr = window.qrcode(0, "M");
      qr.addData(String(text), "Byte");
      qr.make();

      // createSvgTag 支持对象参数：{ cellSize, margin, scalable, alt, title }
      const markup = qr.createSvgTag({ cellSize: 4, margin: 4, scalable: true, alt: "二维码" });
      const wrap = document.createElement("div");
      wrap.innerHTML = markup;
      const svg = wrap.querySelector("svg");
      if (!svg) throw new Error("二维码渲染失败");

      // 固定显示尺寸，保持矢量清晰
      svg.removeAttribute("width");
      svg.removeAttribute("height");
      svg.setAttribute("width", px);
      svg.setAttribute("height", px);
      svg.style.width = "100%";
      svg.style.height = "auto";
      svg.style.maxWidth = px + "px";
      svg.style.display = "block";
      svg.setAttribute("shape-rendering", "crispEdges");

      container.appendChild(svg);
      return true;
    } catch (e) {
      console.error("二维码生成失败", e);
      container.innerHTML = '<p class="hint" style="text-align:center">二维码生成失败，请复制链接使用</p>';
      return false;
    }
  }

  /* ---------------- Base path ---------------- */
  // app.js 由 {{ .basePath }}/static/app.js 加载，可据此反推安全入口前缀
  function detectBasePath() {
    if (window.R2_BASE_PATH !== undefined) return window.R2_BASE_PATH;
    try {
      const script = document.currentScript || Array.prototype.find.call(
        document.querySelectorAll("script[src]"),
        (s) => /\/static\/app\.js/.test(s.getAttribute("src") || "")
      );
      if (script) {
        const src = script.getAttribute("src") || "";
        const idx = src.indexOf("/static/");
        return idx >= 0 ? src.slice(0, idx) : "";
      }
    } catch (e) { /* ignore */ }
    return "";
  }
  const BASE_PATH = detectBasePath();

  /* ---------------- Upload limit ---------------- */
  // 从 /put/info 拉取服务端上传上限（结果缓存），用于上传前预校验
  let uploadLimitCache = null;
  let uploadLimitPromise = null;

  function uploadLimit() {
    if (uploadLimitCache !== null) return Promise.resolve(uploadLimitCache);
    if (uploadLimitPromise) return uploadLimitPromise;
    uploadLimitPromise = fetch(BASE_PATH + "/put/info", { headers: { Accept: "application/json" } })
      .then((r) => r.json())
      .then((d) => {
        uploadLimitCache = (d && d.data && Number(d.data.max_upload_size)) || 0;
        return uploadLimitCache;
      })
      .catch(() => {
        uploadLimitCache = 0;
        return 0;
      });
    return uploadLimitPromise;
  }

  // 返回 null 表示通过，否则返回错误文案
  async function checkUploadSize(file) {
    const limit = await uploadLimit();
    if (limit > 0 && file.size > limit) {
      return "文件超过大小限制（最大 " + formatSize(limit) + "）";
    }
    return null;
  }

  /* ---------------- Upload (XHR 带进度) ---------------- */
  function putFile(url, file, onProgress) {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open("PUT", url, true);
      xhr.setRequestHeader("Content-Type", file.type || "application/octet-stream");
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable && onProgress) onProgress(e.loaded / e.total);
      };
      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) resolve(xhr);
        else reject(new Error("上传失败（HTTP " + xhr.status + "）"));
      };
      xhr.onerror = () => reject(new Error("上传失败，请检查网络"));
      xhr.onabort = () => reject(new Error("上传已取消"));
      xhr.ontimeout = () => reject(new Error("上传超时"));
      xhr.send(file);
    });
  }

  function postFile(url, file, onProgress, extraHeaders) {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open("POST", url, true);
      xhr.setRequestHeader("Content-Type", file.type || "application/octet-stream");
      xhr.setRequestHeader("X-Content-Type", file.type || "application/octet-stream");
      if (extraHeaders) {
        Object.keys(extraHeaders).forEach((k) => xhr.setRequestHeader(k, extraHeaders[k]));
      }
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable && onProgress) onProgress(e.loaded / e.total);
      };
      xhr.onload = () => {
        let data = null;
        try { data = JSON.parse(xhr.responseText); } catch (e) { /* ignore */ }
        if (xhr.status >= 200 && xhr.status < 300) resolve(data);
        else reject(new Error((data && data.error && data.error.message) || "上传失败（HTTP " + xhr.status + "）"));
      };
      xhr.onerror = () => reject(new Error("上传失败，请检查网络"));
      xhr.onabort = () => reject(new Error("上传已取消"));
      xhr.send(file);
    });
  }

  /* ---------------- Misc ---------------- */
  function debounce(fn, wait) {
    let timer = null;
    return function () {
      const args = arguments;
      clearTimeout(timer);
      timer = setTimeout(() => fn.apply(this, args), wait || 300);
    };
  }

  function initials(name) {
    const s = String(name || "?").trim();
    if (!s) return "?";
    const isAscii = /^[\x00-\x7F]+$/.test(s);
    return isAscii ? s.slice(0, 2).toUpperCase() : s.slice(0, 1);
  }

  function setBusy(btn, busy, busyText) {
    if (!btn) return;
    if (busy) {
      btn.dataset.originalText = btn.innerHTML;
      btn.classList.add("is-loading");
      btn.disabled = true;
      if (busyText) btn.textContent = busyText;
    } else {
      btn.classList.remove("is-loading");
      btn.disabled = false;
      if (btn.dataset.originalText) btn.innerHTML = btn.dataset.originalText;
      delete btn.dataset.originalText;
    }
  }

  document.addEventListener("DOMContentLoaded", () => {
    bindModalDismiss();

    // 主题切换按钮：同步图标/文案
    document.querySelectorAll("[data-theme-toggle]").forEach((btn) => {
      btn.addEventListener("click", (e) => {
        e.preventDefault();
        R2.theme.toggle();
        toast("已切换为" + ({ light: "日间模式", dark: "夜间模式", auto: "跟随系统" }[storedTheme()]), "info", 1600);
      });
    });
    onThemeChange((mode, resolved) => {
      document.querySelectorAll("[data-theme-toggle]").forEach((btn) => {
        const label = btn.querySelector(".tt-label");
        if (label && !btn.classList.contains("tabbar-item")) {
          label.textContent = mode === "auto" ? "跟随系统" : mode === "dark" ? "夜间模式" : "日间模式";
        }
        btn.setAttribute("aria-label", "切换主题（当前：" + (mode === "auto" ? "跟随系统" : mode === "dark" ? "夜间" : "日间") + "）");
        btn.setAttribute("title", "当前：" + (mode === "auto" ? "跟随系统" : mode === "dark" ? "夜间模式" : "日间模式"));
        const sun = btn.querySelector(".tt-sun");
        const moon = btn.querySelector(".tt-moon");
        const auto = btn.querySelector(".tt-auto");
        if (sun) sun.style.display = resolved === "dark" ? "none" : "";
        if (moon) moon.style.display = resolved === "dark" ? "" : "none";
        if (auto) auto.style.display = mode === "auto" ? "" : "none";
      });
    });
    const toggle = document.querySelector("[data-menu-toggle]");
    const menu = document.querySelector(".mobile-menu");
    if (toggle && menu) {
      toggle.addEventListener("click", () => menu.classList.toggle("open"));
    }
  });

  window.R2 = {
    toast,
    request,
    formatSize,
    formatTime,
    timeAgo,
    countdown,
    escapeHTML,
    copy,
    openModal,
    closeModal,
    confirm: confirmDialog,
    qrInto,
    putFile,
    postFile,
    uploadLimit,
    checkUploadSize,
    debounce,
    initials,
    setBusy,
    theme: {
      get: storedTheme,
      set: setTheme,
      toggle: toggleTheme,
      apply: applyTheme,
      onChange: onThemeChange,
      resolved: () => resolvedTheme(storedTheme()),
    },
    icons: ICONS,
  };
})();
