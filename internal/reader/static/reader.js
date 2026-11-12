// den-wiki reader: drawers on narrow screens, the contents highlight, and
// typesetting for math (KaTeX) and diagrams (Mermaid) when a page has them.
"use strict";

document.addEventListener("DOMContentLoaded", () => {
  // Drawers: one open at a time; Escape or a click outside closes it.
  const toggles = document.querySelectorAll(".drawer-toggle");
  const close = () => toggles.forEach((t) => {
    t.setAttribute("aria-expanded", "false");
    document.getElementById(t.dataset.drawer)?.classList.remove("open");
  });
  toggles.forEach((t) => t.addEventListener("click", (e) => {
    e.stopPropagation();
    const pane = document.getElementById(t.dataset.drawer);
    const opening = !pane.classList.contains("open");
    close();
    if (opening) {
      pane.classList.add("open");
      t.setAttribute("aria-expanded", "true");
    }
  }));
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") close(); });
  document.querySelector(".main")?.addEventListener("click", close);
  document.querySelectorAll(".aside a[href^='#']").forEach((a) => a.addEventListener("click", close));

  // Topic groups: the toggle expands the group; the title is a link to the
  // group's overview page.
  document.querySelectorAll(".tree-toggle").forEach((b) => b.addEventListener("click", () => {
    const open = b.getAttribute("aria-expanded") !== "true";
    b.setAttribute("aria-expanded", String(open));
    const list = document.getElementById(b.getAttribute("aria-controls"));
    if (list) list.hidden = !open;
  }));

  // Contents: highlight the section being read.
  const links = new Map();
  document.querySelectorAll(".contents a").forEach((a) => links.set(decodeURIComponent(a.hash.slice(1)), a));
  if (links.size && "IntersectionObserver" in window) {
    const headings = [...links.keys()].map((id) => document.getElementById(id)).filter(Boolean);
    let active = null;
    const seen = new Set();
    const observer = new IntersectionObserver((entries) => {
      entries.forEach((e) => (e.isIntersecting ? seen.add(e.target) : seen.delete(e.target)));
      const top = headings.find((h) => seen.has(h));
      if (top && top !== active) {
        active?.classList.remove("active");
        active = links.get(top.id);
        active?.classList.add("active");
      }
    }, { rootMargin: "-10% 0px -70% 0px" });
    headings.forEach((h) => observer.observe(h));
  }

  if (window.katex) {
    document.querySelectorAll(".body .math").forEach((el) => {
      try {
        window.katex.render(el.textContent, el, { displayMode: el.classList.contains("display"), throwOnError: false });
      } catch (err) {
        el.title = String(err);
      }
    });
  }

  if (window.mermaid) {
    const dark = window.matchMedia("(prefers-color-scheme: dark)").matches;
    window.mermaid.initialize({ startOnLoad: false, theme: dark ? "dark" : "default", securityLevel: "strict" });
    window.mermaid.run({ querySelector: ".body pre.mermaid" });
  }
});
