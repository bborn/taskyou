// ty-chrome annotation overlay. Injected on demand; idempotent.
// All UI lives in a shadow root so page styles and ours never mix. Markers are
// real DOM positioned in page coordinates, so chrome.tabs.captureVisibleTab
// bakes them into the screenshot sent to the executor.
(() => {
  if (window.__tyAnnotate) {
    window.__tyAnnotate.show();
    return;
  }

  const TEAL = '#0d9488';
  let mode = 'none'; // none | select | box | note
  let annotations = []; // {kind,label,selector,tag,text,html,rect,styles,comment,els:[]}
  let nextLabel = 1;

  // --- Shadow host -----------------------------------------------------------
  const host = document.createElement('div');
  host.id = 'ty-annotate-host';
  host.style.cssText = 'position:absolute;top:0;left:0;width:0;height:0;z-index:2147483647;';
  document.documentElement.appendChild(host);
  const root = host.attachShadow({ mode: 'open' });

  const style = document.createElement('style');
  style.textContent = `
    * { box-sizing: border-box; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    .toolbar {
      position: fixed; bottom: 16px; left: 50%; transform: translateX(-50%);
      display: flex; gap: 6px; align-items: center; padding: 8px 10px;
      background: #111827; color: #f9fafb; border-radius: 999px;
      box-shadow: 0 4px 24px rgba(0,0,0,.35); font-size: 13px;
    }
    .toolbar button {
      border: 0; border-radius: 999px; padding: 6px 12px; cursor: pointer;
      background: #1f2937; color: #f9fafb; font-size: 13px;
    }
    .toolbar button:hover { background: #374151; }
    .toolbar button.active { background: ${TEAL}; color: #fff; }
    .toolbar button.send { background: ${TEAL}; font-weight: 600; }
    .toolbar button.send:disabled { background: #1f2937; color: #6b7280; cursor: default; }
    .toolbar .count { background: #1f2937; border-radius: 999px; padding: 4px 10px; color: #9ca3af; }
    .toolbar .close { background: transparent; color: #9ca3af; padding: 6px 8px; }
    .hl { position: fixed; pointer-events: none; border: 2px solid ${TEAL};
      background: rgba(13,148,136,.08); border-radius: 2px; display: none; }
    .marker {
      position: absolute; width: 22px; height: 22px; border-radius: 50%;
      background: ${TEAL}; color: #fff; font-size: 12px; font-weight: 700;
      display: flex; align-items: center; justify-content: center;
      box-shadow: 0 1px 4px rgba(0,0,0,.4); cursor: pointer; user-select: none;
    }
    .region { position: absolute; border: 2px dashed ${TEAL}; background: rgba(13,148,136,.12); border-radius: 2px; }
    .dragrect { position: fixed; border: 2px dashed ${TEAL}; background: rgba(13,148,136,.12); display: none; }
    .boxlayer { position: fixed; inset: 0; cursor: crosshair; }
    .popover {
      position: fixed; width: 260px; background: #fff; border-radius: 10px;
      box-shadow: 0 8px 30px rgba(0,0,0,.3); padding: 10px; font-size: 13px; color: #111827;
    }
    .popover textarea {
      width: 100%; height: 64px; border: 1px solid #d1d5db; border-radius: 6px;
      padding: 6px; font-size: 13px; resize: vertical; outline-color: ${TEAL};
    }
    .popover .row { display: flex; justify-content: flex-end; gap: 6px; margin-top: 8px; }
    .popover button { border: 0; border-radius: 6px; padding: 5px 12px; cursor: pointer; font-size: 13px; }
    .popover .save { background: ${TEAL}; color: #fff; }
    .popover .cancel { background: #e5e7eb; }
    .popover .del { background: #fee2e2; color: #b91c1c; margin-right: auto; }
    .popover .meta { color: #6b7280; font-size: 11px; margin-bottom: 6px; word-break: break-all; }
    .toast {
      position: fixed; bottom: 64px; left: 50%; transform: translateX(-50%);
      background: #111827; color: #f9fafb; border-radius: 8px; padding: 8px 14px;
      font-size: 13px; box-shadow: 0 4px 24px rgba(0,0,0,.35); white-space: nowrap;
    }
  `;
  root.appendChild(style);

  // Page-coordinate layer for markers / region rectangles.
  const layer = document.createElement('div');
  layer.style.cssText = 'position:absolute;top:0;left:0;width:0;height:0;overflow:visible;';
  root.appendChild(layer);

  const hl = el('div', 'hl');
  root.appendChild(hl);

  // --- Toolbar ---------------------------------------------------------------
  const toolbar = el('div', 'toolbar');
  const btnSelect = button('Select', () => setMode(mode === 'select' ? 'none' : 'select'));
  const btnBox = button('Box', () => setMode(mode === 'box' ? 'none' : 'box'));
  const btnNote = button('Note', () => setMode(mode === 'note' ? 'none' : 'note'));
  const countChip = el('span', 'count');
  const btnSend = button('Send', doSend);
  btnSend.classList.add('send');
  const btnClose = button('✕', teardownOrHide);
  btnClose.classList.add('close');
  btnClose.title = 'Clear annotations and hide';
  toolbar.append(btnSelect, btnBox, btnNote, countChip, btnSend, btnClose);
  root.appendChild(toolbar);
  updateCount();

  function el(tag, cls) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    return e;
  }
  function button(label, onClick) {
    const b = el('button');
    b.textContent = label;
    b.addEventListener('click', onClick);
    return b;
  }

  // --- Modes -----------------------------------------------------------------
  const boxLayer = el('div', 'boxlayer');
  const dragRect = el('div', 'dragrect');

  function setMode(m) {
    mode = m;
    btnSelect.classList.toggle('active', m === 'select');
    btnBox.classList.toggle('active', m === 'box');
    btnNote.classList.toggle('active', m === 'note');
    hl.style.display = 'none';
    boxLayer.remove();
    dragRect.remove();
    if (m === 'box') {
      root.appendChild(boxLayer);
      root.appendChild(dragRect);
    }
    if (m === 'note') {
      openPopover({ kind: 'note', viewportX: innerWidth / 2 - 130, viewportY: innerHeight / 3 });
      // popover handles the rest; drop back to neutral
      mode = 'none';
      btnNote.classList.remove('active');
    }
  }

  // Select mode: hover highlight + capture-phase click interception.
  function onMouseOver(e) {
    if (mode !== 'select') return;
    const t = realTarget(e);
    if (!t) return;
    const r = t.getBoundingClientRect();
    hl.style.display = 'block';
    hl.style.left = r.left - 2 + 'px';
    hl.style.top = r.top - 2 + 'px';
    hl.style.width = r.width + 4 + 'px';
    hl.style.height = r.height + 4 + 'px';
  }
  function onClick(e) {
    if (mode !== 'select') return;
    const t = realTarget(e);
    if (!t) return;
    e.preventDefault();
    e.stopPropagation();
    hl.style.display = 'none';
    const snap = snapshotElement(t);
    const r = t.getBoundingClientRect();
    openPopover({ ...snap, viewportX: Math.min(r.right + 8, innerWidth - 280), viewportY: Math.max(r.top, 8) });
    setMode('none');
  }
  function realTarget(e) {
    if (e.composedPath().includes(host)) return null;
    const t = e.target;
    return t && t.nodeType === 1 ? t : null;
  }
  document.addEventListener('mouseover', onMouseOver, true);
  document.addEventListener('click', onClick, true);
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && mode !== 'none') {
      e.stopPropagation();
      setMode('none');
    }
  }, true);

  // Box mode drag handling.
  let dragStart = null;
  boxLayer.addEventListener('mousedown', (e) => {
    dragStart = { x: e.clientX, y: e.clientY };
    dragRect.style.display = 'block';
    sizeDragRect(e);
  });
  boxLayer.addEventListener('mousemove', (e) => {
    if (dragStart) sizeDragRect(e);
  });
  boxLayer.addEventListener('mouseup', (e) => {
    if (!dragStart) return;
    const r = normRect(dragStart, { x: e.clientX, y: e.clientY });
    dragStart = null;
    dragRect.style.display = 'none';
    setMode('none');
    if (r.w < 8 || r.h < 8) return;
    openPopover({
      kind: 'region',
      rect: { x: r.x + scrollX, y: r.y + scrollY, w: r.w, h: r.h },
      viewportX: Math.min(r.x + r.w + 8, innerWidth - 280),
      viewportY: Math.max(r.y, 8),
    });
  });
  function sizeDragRect(e) {
    const r = normRect(dragStart, { x: e.clientX, y: e.clientY });
    Object.assign(dragRect.style, { left: r.x + 'px', top: r.y + 'px', width: r.w + 'px', height: r.h + 'px' });
  }
  function normRect(a, b) {
    return { x: Math.min(a.x, b.x), y: Math.min(a.y, b.y), w: Math.abs(a.x - b.x), h: Math.abs(a.y - b.y) };
  }

  // --- Element snapshot ------------------------------------------------------
  function cssPath(elem) {
    if (elem.id) return `#${CSS.escape(elem.id)}`;
    const parts = [];
    let cur = elem;
    while (cur && cur.nodeType === 1 && cur !== document.body) {
      let part = cur.localName;
      const stable = [...cur.classList].filter((c) => /^[a-zA-Z][\w-]*$/.test(c)).slice(0, 2);
      if (stable.length) part += '.' + stable.map(CSS.escape).join('.');
      const siblings = cur.parentElement
        ? [...cur.parentElement.children].filter((s) => s.localName === cur.localName)
        : [];
      if (siblings.length > 1) part += `:nth-of-type(${siblings.indexOf(cur) + 1})`;
      parts.unshift(part);
      if (cur.parentElement && cur.parentElement.id) {
        parts.unshift(`#${CSS.escape(cur.parentElement.id)}`);
        break;
      }
      cur = cur.parentElement;
    }
    return parts.join(' > ');
  }

  const STYLE_KEYS = ['color', 'backgroundColor', 'fontSize', 'fontWeight', 'fontFamily', 'display', 'position', 'margin', 'padding'];
  function snapshotElement(elem) {
    const cs = getComputedStyle(elem);
    const r = elem.getBoundingClientRect();
    return {
      kind: 'element',
      selector: cssPath(elem),
      tag: elem.localName,
      text: (elem.innerText || '').trim().slice(0, 200),
      html: elem.outerHTML.length > 1500 ? elem.outerHTML.slice(0, 1500) + '…' : elem.outerHTML,
      rect: { x: r.x + scrollX, y: r.y + scrollY, w: r.width, h: r.height },
      styles: Object.fromEntries(STYLE_KEYS.map((k) => [k, cs[k]])),
    };
  }

  // --- Comment popover -------------------------------------------------------
  let popover = null;
  function closePopover() {
    popover?.remove();
    popover = null;
  }
  // draft: annotation fields (without label/comment) + viewportX/Y for placement.
  // existing: pass an annotation object to view/edit/delete it instead.
  function openPopover(draft, existing) {
    closePopover();
    popover = el('div', 'popover');
    Object.assign(popover.style, { left: draft.viewportX + 'px', top: draft.viewportY + 'px' });

    const meta = el('div', 'meta');
    const a = existing || draft;
    meta.textContent = a.kind === 'element' ? `<${a.tag}> ${a.selector}` : a.kind === 'region' ? 'Region' : 'Note';
    const ta = el('textarea');
    ta.placeholder = 'What should change here?';
    ta.value = existing?.comment || '';
    const row = el('div', 'row');
    const save = button(existing ? 'Update' : 'Save', () => {
      const comment = ta.value.trim();
      if (!comment) return;
      if (existing) {
        existing.comment = comment;
      } else {
        addAnnotation({ ...draft, comment });
      }
      closePopover();
      updateCount();
    });
    save.className = 'save';
    const cancel = button('Cancel', closePopover);
    cancel.className = 'cancel';
    if (existing) {
      const del = button('Remove', () => {
        removeAnnotation(existing);
        closePopover();
      });
      del.className = 'del';
      row.append(del);
    }
    row.append(cancel, save);
    popover.append(meta, ta, row);
    root.appendChild(popover);
    ta.focus();
    ta.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) save.click();
      if (e.key === 'Escape') closePopover();
      e.stopPropagation();
    });
  }

  // --- Annotation store + markers ---------------------------------------------
  function addAnnotation(a) {
    a.label = nextLabel++;
    a.els = [];
    const noteStack = annotations.filter((x) => x.kind === 'note').length;
    let mx, my;
    if (a.kind === 'note') {
      mx = scrollX + 10;
      my = scrollY + 10 + noteStack * 28;
    } else {
      mx = a.rect.x + a.rect.w - 11;
      my = a.rect.y - 11;
    }
    if (a.kind === 'region') {
      const rg = el('div', 'region');
      Object.assign(rg.style, { left: a.rect.x + 'px', top: a.rect.y + 'px', width: a.rect.w + 'px', height: a.rect.h + 'px' });
      layer.appendChild(rg);
      a.els.push(rg);
    }
    const m = el('div', 'marker');
    m.textContent = a.label;
    m.title = a.comment;
    Object.assign(m.style, { left: mx + 'px', top: Math.max(my, 0) + 'px' });
    m.addEventListener('click', (e) => {
      const r = m.getBoundingClientRect();
      openPopover({ viewportX: Math.min(r.right + 6, innerWidth - 280), viewportY: r.top, kind: a.kind, tag: a.tag, selector: a.selector }, a);
      e.stopPropagation();
    });
    layer.appendChild(m);
    a.els.push(m);
    annotations.push(a);
    updateCount();
  }

  function removeAnnotation(a) {
    a.els.forEach((e) => e.remove());
    annotations = annotations.filter((x) => x !== a);
    updateCount();
  }

  function clearAll() {
    annotations.forEach((a) => a.els.forEach((e) => e.remove()));
    annotations = [];
    nextLabel = 1;
    updateCount();
  }

  function updateCount() {
    countChip.textContent = `${annotations.length} pinned`;
    btnSend.disabled = annotations.length === 0;
    chrome.runtime.sendMessage({ type: 'annotationsChanged', count: annotations.length }).catch(() => {});
  }

  // --- Collect + send ----------------------------------------------------------
  function collect() {
    return {
      url: location.href,
      title: document.title,
      viewport: { width: innerWidth, height: innerHeight, dpr: devicePixelRatio },
      annotations: annotations.map(({ els, ...a }) => a),
    };
  }

  let toastEl = null;
  function toast(msg, ms = 3500) {
    toastEl?.remove();
    toastEl = el('div', 'toast');
    toastEl.textContent = msg;
    root.appendChild(toastEl);
    setTimeout(() => toastEl?.remove(), ms);
  }

  async function doSend() {
    if (!annotations.length) return;
    closePopover();
    toolbar.style.visibility = 'hidden'; // keep markers, hide chrome, in the screenshot
    await new Promise((r) => setTimeout(r, 80));
    let resp;
    try {
      resp = await chrome.runtime.sendMessage({ type: 'sendAnnotations', payload: collect() });
    } catch (e) {
      resp = { error: e.message };
    }
    toolbar.style.visibility = 'visible';
    if (resp?.ok) {
      toast(`Sent to task #${resp.taskId}${resp.nudged ? ' — executor nudged ✓' : ' (no live executor; bundle saved)'}`);
    } else {
      toast(resp?.error || 'Send failed', 5000);
    }
  }

  function teardownOrHide() {
    clearAll();
    setMode('none');
    closePopover();
    host.style.display = 'none';
  }

  // --- Messages from SW / side panel -------------------------------------------
  chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
    switch (msg?.type) {
      case 'ty-enter-select':
        host.style.display = '';
        setMode('select');
        sendResponse({ ok: true });
        break;
      case 'ty-collect':
        sendResponse(collect());
        break;
      case 'ty-clear':
        clearAll();
        sendResponse({ ok: true });
        break;
      case 'ty-get-count':
        sendResponse({ count: annotations.length });
        break;
      default:
        return false;
    }
    return false;
  });

  window.__tyAnnotate = {
    show() {
      host.style.display = '';
      setMode('select');
    },
  };
})();
