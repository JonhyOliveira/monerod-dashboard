"use strict";

// Pages are rendered on the server. This script only:
//  - refreshes the page's #live section through htmx on a timer, driving
//    the refresh ring in the header;
//  - turns forms marked data-action into htmx posts (a toast plus a live
//    refresh instead of a full page load);
//  - shows <time data-local> values in the viewer's timezone;
//  - dismisses toasts.
// Everything works without it, just with full page loads.

(() => {
  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

  // ---- Local times ----
  function localize(root) {
    for (const el of $$("time[data-local]", root)) {
      const d = new Date(el.dateTime);
      if (!isNaN(d)) el.textContent = d.toLocaleString();
    }
  }

  // ---- Action forms ----
  function enhance(root) {
    for (const form of $$("form[data-action]", root)) {
      if (form.dataset.enhanced) continue;
      form.dataset.enhanced = "1";
      form.setAttribute("hx-post", form.getAttribute("action"));
      form.setAttribute("hx-target", "#toasts");
      form.setAttribute("hx-swap", "afterbegin");
      if (form.dataset.confirm) form.setAttribute("hx-confirm", form.dataset.confirm);
      // Buttons that post elsewhere or need their own confirmation handle the
      // request themselves, with the form's values.
      for (const btn of $$("button[formaction], button[data-confirm]", form)) {
        btn.setAttribute("hx-post", btn.getAttribute("formaction") || form.getAttribute("action"));
        btn.setAttribute("hx-target", "#toasts");
        btn.setAttribute("hx-swap", "afterbegin");
        if (btn.dataset.confirm) btn.setAttribute("hx-confirm", btn.dataset.confirm);
      }
      htmx.process(form);
    }
    // "Select all" checkboxes in tables.
    for (const all of $$("input[data-select-all]", root)) {
      all.addEventListener("change", () => {
        for (const cb of $$(`input[name="${all.dataset.selectAll}"]`, all.form)) cb.checked = all.checked;
      });
    }
  }

  // Clear forms that succeeded, except ones that show current settings.
  document.addEventListener("htmx:afterRequest", (e) => {
    const form = e.detail.elt.closest && e.detail.elt.closest("form[data-action]");
    if (!form || !e.detail.successful) return;
    const failed = e.detail.xhr.responseText.includes("toast error");
    if (!failed) for (const input of $$('input[name="confirm"]', form)) input.value = "";
  });

  // ---- Toasts ----
  function armToasts(root) {
    for (const t of $$(".toast", root)) {
      if (t.dataset.armed) continue;
      t.dataset.armed = "1";
      const ms = t.classList.contains("error") ? 12000 : 6000;
      setTimeout(() => t.classList.add("leaving"), ms);
      t.addEventListener("animationend", (e) => { if (e.animationName === "toast-out") t.remove(); });
      t.addEventListener("click", () => t.remove());
    }
  }

  // ---- Live refresh + ring ----
  const live = $("#live[data-live]");
  const ring = $("#refresh");
  let refreshSeconds = ring ? parseFloat(getComputedStyle(ring).getPropertyValue("--refresh-duration")) || 5 : 5;
  let timer, nextAt = 0, inflight = false, failed = false, paused = false;

  function setRing(state) {
    if (!ring) return;
    ring.classList.remove("counting", "fetching", "paused");
    if (state === "counting") void ring.offsetWidth; // restart the fill animation
    ring.classList.add(state);
    ring.classList.toggle("error", failed);
  }

  function tick() {
    if (!ring) return;
    const left = Math.max(0, Math.ceil((nextAt - Date.now()) / 1000));
    const label = inflight ? "Refreshing…" : paused ? "Paused while you work in the list" : `Next refresh in ${left}s`;
    ring.title = label;
    ring.setAttribute("aria-label", label + (inflight ? "" : " (click to refresh now)"));
    $(".refresh-text", ring).textContent = inflight ? "…" : paused ? "Ⅱ" : `${left}s`;
  }

  function schedule() {
    clearTimeout(timer);
    nextAt = Date.now() + refreshSeconds * 1000;
    timer = setTimeout(() => refresh(false), refreshSeconds * 1000);
    setRing("counting");
    tick();
  }

  // Don't swap the live section out from under someone selecting rows or
  // typing in it.
  // Also hold off once more rows were loaded by scrolling: a refresh would
  // swap them away. The ring shows "paused"; clicking it refreshes anyway.
  function busy() {
    const a = document.activeElement;
    return (live.contains(a) && a.matches("input, select, textarea"))
      || $$("input[type=checkbox]:checked", live).length > 0
      || $("tr[data-extra], .more-rows.htmx-request", live) !== null
      || Date.now() - scrolledAt < 1500;
  }

  function refresh(force) {
    if (!live || inflight) return;
    if (!force && busy()) {
      paused = true;
      setRing("paused");
      tick();
      clearTimeout(timer);
      timer = setTimeout(() => refresh(false), 1000);
      return;
    }
    paused = false;
    inflight = true;
    clearTimeout(timer);
    setRing("fetching");
    tick();
    htmx.ajax("GET", location.pathname + location.search, { target: "#live", swap: "innerHTML" });
  }

  // A refresh replaces the section's HTML, which would reset every scroll
  // box to its start and reopen closed <details>. Carry both across the
  // swap, matching elements by their position in the section. (afterSwap
  // fires more than once per refresh, so restoring is idempotent.)
  let kept = null;
  document.addEventListener("htmx:beforeSwap", (e) => {
    if (!live || e.detail.target !== live) return;
    kept = {
      scroll: $$(".scroll", live).map((el) => [el.scrollLeft, el.scrollTop]),
      open: $$("details", live).map((el) => el.open),
    };
  });
  document.addEventListener("htmx:afterSwap", (e) => {
    if (!live || e.detail.target !== live || !kept) return;
    $$(".scroll", live).forEach((el, i) => {
      if (kept.scroll[i]) [el.scrollLeft, el.scrollTop] = kept.scroll[i];
    });
    $$("details", live).forEach((el, i) => {
      if (i < kept.open.length) el.open = kept.open[i];
    });
  });

  // Don't swap a table out from under someone scrolling it: a swap would
  // cut a touch scroll short.
  let scrolledAt = 0;
  live && live.addEventListener("scroll", () => { scrolledAt = Date.now(); }, { capture: true, passive: true });

  document.addEventListener("htmx:afterRequest", (e) => {
    if (!live || e.detail.target !== live || !inflight) return;
    inflight = false;
    failed = !e.detail.successful;
    if (failed) {
      const t = document.createElement("div");
      t.className = "toast error";
      t.textContent = "Dashboard server unreachable; retrying.";
      if (!$("#toasts .toast.error")) $("#toasts").prepend(t), armToasts($("#toasts"));
    }
    schedule();
  });
  document.addEventListener("htmx:sendError", (e) => {
    if (live && e.detail.target === live) { inflight = false; failed = true; schedule(); }
  });

  // Actions announce a change; show it right away.
  document.body.addEventListener("live-refresh", () => refresh(true));

  htmx.onLoad((elt) => { localize(elt); enhance(elt); armToasts(elt); });

  if (live) {
    ring && ring.addEventListener("click", () => refresh(true));
    schedule();
    setInterval(tick, 1000);
  }
})();
