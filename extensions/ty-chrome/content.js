// ty-chrome annotation overlay. Injected on demand; idempotent.
// All UI lives in a shadow root so page styles and ours never mix. Markers are
// real DOM positioned in page coordinates, so chrome.tabs.captureVisibleTab
// bakes them into the screenshot sent to the executor.
(() => {
  if (window.__tyAnnotate) return; // idempotent; visibility is message-driven

  const TEAL = '#d05010'; // taskyou logo orange (accent)
  let mode = 'none'; // none | select | box | note
  let selectDown = false; // a select-mode pointer press is in progress
  let annotations = []; // {kind,label,selector,tag,text,html,rect,styles,comment,els:[]}
  let nextLabel = 1;

  // Floating UI (vendored, injected before us into this isolated world). Used to
  // anchor the comment popover so it flips/shifts to stay fully on-screen.
  const FUI = globalThis.FloatingUIDOM || {};

  // --- Shadow host -----------------------------------------------------------
  // We must paint above EVERYTHING on the page, including modals/overlays that
  // already use the max 32-bit z-index (2147483647) — z-index alone can't win
  // that tie, and it loses outright to elements in the browser's top layer
  // (Fullscreen API, <dialog>, popovers). So we promote the host into the top
  // layer via the Popover API when available; the top layer always paints above
  // normal content. We keep z-index as a fallback for older Chrome.
  const host = document.createElement('div');
  host.id = 'ty-annotate-host';
  const TOP_LAYER = typeof host.showPopover === 'function';
  // inset:auto + explicit 0/0 size overrides the centred-box popover UA styles;
  // overflow:visible lets the fixed toolbar/markers escape the 0×0 host.
  host.style.cssText =
    'position:fixed;inset:auto;top:0;left:0;width:0;height:0;z-index:2147483647;' +
    'border:0;margin:0;padding:0;background:transparent;color:inherit;overflow:visible;';
  if (TOP_LAYER) host.setAttribute('popover', 'manual');
  else host.style.display = 'none'; // shown via ty-enter-select; bridge injection stays invisible
  document.documentElement.appendChild(host);
  const root = host.attachShadow({ mode: 'open' });

  // Visibility is message-driven. In top-layer mode show/hide via the Popover
  // API (which controls `display` itself); otherwise fall back to display.
  let visible = false;
  function showHost() {
    if (TOP_LAYER) {
      try { host.showPopover(); } catch (_) { /* already open */ }
    } else {
      host.style.display = '';
    }
    visible = true;
    syncScroll();
  }
  function hideHost() {
    if (TOP_LAYER) {
      try { host.hidePopover(); } catch (_) { /* already closed */ }
    } else {
      host.style.display = 'none';
    }
    visible = false;
  }

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
      touch-action: manipulation;
    }
    .toolbar button:hover { background: #374151; }
    .toolbar button.active { background: ${TEAL}; color: #fff; }
    .toolbar button.send { background: ${TEAL}; font-weight: 600; }
    .toolbar button.send:disabled { background: #1f2937; color: #6b7280; cursor: default; }
    .toolbar .count { background: #1f2937; border-radius: 999px; padding: 4px 10px; color: #9ca3af; }
    .toolbar .close { background: transparent; color: #9ca3af; padding: 6px 8px; }
    .hl { position: fixed; pointer-events: none; border: 2px solid ${TEAL};
      background: rgba(208,80,16,.08); border-radius: 2px; display: none; }
    .marker {
      position: absolute; width: 22px; height: 22px; border-radius: 50%;
      background: ${TEAL}; color: #fff; font-size: 12px; font-weight: 700;
      display: flex; align-items: center; justify-content: center;
      box-shadow: 0 1px 4px rgba(0,0,0,.4); cursor: pointer; user-select: none;
      touch-action: none;
    }
    .region { position: absolute; border: 2px dashed ${TEAL}; background: rgba(208,80,16,.12); border-radius: 2px; touch-action: none; }
    .dragrect { position: fixed; border: 2px dashed ${TEAL}; background: rgba(208,80,16,.12); display: none; }
    .boxlayer, .selectlayer { position: fixed; inset: 0; cursor: crosshair; touch-action: none; }
    /* The comment editor is a <dialog> opened with showModal() so it joins the
       top layer as the topmost modal — that makes any page-level modal dialog
       (and its focus trap) inert instead of us, so our textarea stays typeable.
       Reset the UA modal centring/border; Floating UI sets left/top. */
    .popover {
      position: fixed; inset: auto; margin: 0; max-height: none; border: 0;
      width: 260px; background: #fff; border-radius: 10px;
      box-shadow: 0 8px 30px rgba(0,0,0,.3); padding: 10px; font-size: 13px; color: #111827;
    }
    .popover::backdrop { background: transparent; }
    .popover textarea {
      width: 100%; height: 64px; border: 1px solid #d1d5db; border-radius: 6px;
      padding: 6px; font-size: 13px; resize: vertical; outline-color: ${TEAL};
    }
    .popover .row { display: flex; justify-content: flex-end; gap: 6px; margin-top: 8px; }
    .popover button { border: 0; border-radius: 6px; padding: 5px 12px; cursor: pointer; font-size: 13px; touch-action: manipulation; }
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

  // Page-coordinate layer for markers / region rectangles. The host is now
  // viewport-fixed (top layer), so this layer — which positions its children in
  // document coordinates — is translated by the scroll offset to keep markers
  // glued to their page elements.
  const layer = document.createElement('div');
  layer.style.cssText = 'position:absolute;top:0;left:0;width:0;height:0;overflow:visible;will-change:transform;';
  root.appendChild(layer);

  function syncScroll() {
    layer.style.transform = `translate(${-scrollX}px, ${-scrollY}px)`;
  }
  window.addEventListener('scroll', syncScroll, { passive: true });
  window.addEventListener('resize', syncScroll, { passive: true });

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
  btnSelect.title = 'Pick an element — S';
  btnBox.title = 'Draw a region — B';
  btnNote.title = 'Page-level note — N';
  btnSend.title = 'Send to executor — ⌘↩ (or ⌥S anywhere)';
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
  const selectLayer = el('div', 'selectlayer');

  function setMode(m) {
    mode = m;
    btnSelect.classList.toggle('active', m === 'select');
    btnBox.classList.toggle('active', m === 'box');
    btnNote.classList.toggle('active', m === 'note');
    hl.style.display = 'none';
    selectDown = false;
    boxLayer.remove();
    dragRect.remove();
    selectLayer.remove();
    if (m === 'select') root.appendChild(selectLayer);
    if (m === 'box') {
      root.appendChild(boxLayer);
      root.appendChild(dragRect);
    }
    if (m === 'note') {
      openPopover({ kind: 'note' });
      // popover handles the rest; drop back to neutral
      mode = 'none';
      btnNote.classList.remove('active');
    }
  }

  // Select mode runs on a full-viewport capture layer (like box mode) so one
  // path serves both a mouse and Chrome's mobile/touch emulation. Touch only
  // dispatches pointer events while pressed and never fires plain hover, so we
  // preview on press-drag and commit on release; a mouse still previews on
  // hover (pointermove fires with no button) and commits on click. The layer's
  // touch-action:none stops the page from stealing the gesture to scroll, and
  // it intercepts the click so the page never navigates.
  //
  // Hit-testing goes through elementsFromPoint (coordinate-based), not e.target:
  // the layer is the event target, and on touch implicit pointer capture would
  // otherwise pin e.target to the press element for the whole drag. We skip our
  // own shadow host (surfaced by the shadow boundary) and take the topmost page
  // element beneath it.
  function pageElementAt(x, y) {
    for (const node of document.elementsFromPoint(x, y)) {
      if (node === host || host.contains(node)) continue;
      return node.nodeType === 1 ? node : null;
    }
    return null;
  }
  function highlight(t) {
    const r = t.getBoundingClientRect();
    hl.style.display = 'block';
    hl.style.left = r.left - 2 + 'px';
    hl.style.top = r.top - 2 + 'px';
    hl.style.width = r.width + 4 + 'px';
    hl.style.height = r.height + 4 + 'px';
  }
  selectLayer.addEventListener('pointermove', (e) => {
    // Mouse previews on hover; touch only tracks while a finger is down.
    if (e.pointerType !== 'mouse' && !selectDown) return;
    const t = pageElementAt(e.clientX, e.clientY);
    if (t) highlight(t);
  });
  selectLayer.addEventListener('pointerdown', (e) => {
    e.preventDefault();
    selectLayer.setPointerCapture?.(e.pointerId);
    selectDown = true;
    const t = pageElementAt(e.clientX, e.clientY);
    if (t) highlight(t);
  });
  selectLayer.addEventListener('pointerup', (e) => {
    if (!selectDown) return;
    selectDown = false;
    const t = pageElementAt(e.clientX, e.clientY);
    hl.style.display = 'none';
    if (!t) {
      setMode('none');
      return;
    }
    const snap = snapshotElement(t);
    openPopover({ ...snap, anchorEl: t });
    setMode('none');
  });

  // Focus shield. Many pages run a focus trap (modal <dialog> controllers, etc.)
  // that refocuses themselves whenever focus appears to leave them. Our UI lives
  // in a shadow host, so focus landing in our comment textarea surfaces to the
  // page as a focus event retargeted to the host — outside their dialog — and the
  // trap yanks focus straight back, making the textarea un-typeable. We can't know
  // what arbitrary pages do, so swallow focus events that originate inside our
  // host at window-capture: that's earlier in the propagation path than any
  // document-level listener, so the page never reacts to focus within our overlay.
  // (Stopping a focus *notification* doesn't stop the focus itself.)
  const swallowOwnFocus = (e) => {
    if (e.composedPath().includes(host)) e.stopImmediatePropagation();
  };
  for (const type of ['focusin', 'focusout', 'focus', 'blur']) {
    window.addEventListener(type, swallowOwnFocus, true);
  }

  // Shortcuts while the overlay is up: S/B/N switch modes, Esc exits,
  // Cmd/Ctrl+Enter sends. Never fire while typing in any input.
  function isTyping(e) {
    const t = e.composedPath()[0];
    return t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable);
  }
  document.addEventListener('keydown', (e) => {
    if (!visible) return;
    if (e.key === 'Escape' && mode !== 'none' && !isTyping(e)) {
      e.stopPropagation();
      setMode('none');
      return;
    }
    if (isTyping(e) || popover) return;
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault();
      doSend();
      return;
    }
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    const k = e.key.toLowerCase();
    if (k === 's') setMode('select');
    else if (k === 'b') setMode('box');
    else if (k === 'n') setMode('note');
    else return;
    e.preventDefault();
    e.stopPropagation();
  }, true);

  // Box mode drag handling. Pointer events (not mouse) so the drag works with
  // a touch/pen as well as a mouse — Chrome's mobile responsive emulation
  // dispatches pointer/touch, not mouse, events. Pointer capture keeps the
  // gesture bound to the layer even if it leaves the surface; touch-action:none
  // (set in CSS) stops the browser stealing the drag to scroll/zoom.
  let dragStart = null;
  boxLayer.addEventListener('pointerdown', (e) => {
    e.preventDefault();
    boxLayer.setPointerCapture?.(e.pointerId);
    dragStart = { x: e.clientX, y: e.clientY };
    dragRect.style.display = 'block';
    sizeDragRect(e);
  });
  boxLayer.addEventListener('pointermove', (e) => {
    if (dragStart) sizeDragRect(e);
  });
  boxLayer.addEventListener('pointerup', (e) => {
    if (!dragStart) return;
    const r = normRect(dragStart, { x: e.clientX, y: e.clientY });
    dragStart = null;
    dragRect.style.display = 'none';
    setMode('none');
    if (r.w < 8 || r.h < 8) return;
    openPopover({
      kind: 'region',
      rect: { x: r.x + scrollX, y: r.y + scrollY, w: r.w, h: r.h },
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
  let provisionalEl = null;

  // Keep the drawn box / picked element visible while the comment popover is
  // open. Region boxes stay interactive (drag to move, corner handles to
  // resize) until saved; Save reads the final geometry, Cancel removes it.
  function showProvisional(draft, interactive) {
    if (!draft.rect || (draft.kind !== 'region' && draft.kind !== 'element')) return;
    provisionalEl = el('div', 'region');
    if (draft.kind === 'element') {
      provisionalEl.style.borderStyle = 'solid';
      provisionalEl.style.background = 'rgba(208,80,16,.06)';
    }
    Object.assign(provisionalEl.style, {
      left: draft.rect.x + 'px',
      top: draft.rect.y + 'px',
      width: draft.rect.w + 'px',
      height: draft.rect.h + 'px',
    });
    if (interactive) makeInteractive(provisionalEl);
    layer.appendChild(provisionalEl);
  }

  function readProvisionalRect() {
    return {
      x: parseFloat(provisionalEl.style.left),
      y: parseFloat(provisionalEl.style.top),
      w: parseFloat(provisionalEl.style.width),
      h: parseFloat(provisionalEl.style.height),
    };
  }

  function makeInteractive(box) {
    box.style.cursor = 'move';
    const corners = [
      ['nw', 'left:-7px;top:-7px;cursor:nwse-resize'],
      ['ne', 'right:-7px;top:-7px;cursor:nesw-resize'],
      ['sw', 'left:-7px;bottom:-7px;cursor:nesw-resize'],
      ['se', 'right:-7px;bottom:-7px;cursor:nwse-resize'],
    ];
    for (const [name, pos] of corners) {
      const h = el('div');
      h.dataset.corner = name;
      h.style.cssText = `position:absolute;width:12px;height:12px;background:#fff;border:2px solid ${TEAL};border-radius:50%;${pos}`;
      box.appendChild(h);
    }
    // Pointer events (not mouse) so move/resize works with touch + pen under
    // Chrome's mobile emulation. Pointer capture routes the move/up stream to
    // the box even when the pointer leaves it; touch-action:none (CSS) stops
    // the gesture being consumed as a page scroll.
    box.addEventListener('pointerdown', (e) => {
      e.preventDefault();
      e.stopPropagation();
      const corner = e.target.dataset?.corner || '';
      box.setPointerCapture?.(e.pointerId);
      const start = {
        sx: e.clientX, sy: e.clientY,
        x: parseFloat(box.style.left), y: parseFloat(box.style.top),
        w: parseFloat(box.style.width), h: parseFloat(box.style.height),
      };
      const onMove = (ev) => {
        const dx = ev.clientX - start.sx;
        const dy = ev.clientY - start.sy;
        let { x, y, w, h } = start;
        if (!corner) {
          x += dx;
          y += dy;
        } else {
          if (corner.includes('w')) { x = Math.min(start.x + dx, start.x + start.w - 10); w = Math.max(10, start.w - dx); }
          if (corner.includes('e')) { w = Math.max(10, start.w + dx); }
          if (corner.includes('n')) { y = Math.min(start.y + dy, start.y + start.h - 10); h = Math.max(10, start.h - dy); }
          if (corner.includes('s')) { h = Math.max(10, start.h + dy); }
        }
        Object.assign(box.style, { left: x + 'px', top: y + 'px', width: w + 'px', height: h + 'px' });
      };
      const onUp = () => {
        document.removeEventListener('pointermove', onMove, true);
        document.removeEventListener('pointerup', onUp, true);
      };
      document.addEventListener('pointermove', onMove, true);
      document.addEventListener('pointerup', onUp, true);
    });
  }

  let editingHidden = null; // annotation whose visuals are hidden while editing
  let stopAutoPosition = null; // Floating UI autoUpdate cleanup

  function closePopover() {
    stopAutoPosition?.();
    stopAutoPosition = null;
    const p = popover;
    popover = null;
    if (p) {
      if (p.open) try { p.close(); } catch (_) {} // release the top layer
      p.remove();
    }
    provisionalEl?.remove();
    provisionalEl = null;
    editingHidden?.els.forEach((e) => (e.style.display = ''));
    editingHidden = null;
  }

  // Anchor the popover to `reference` (a real element or a virtual {getBoundingClientRect})
  // and keep it on-screen: offset off the anchor, flip to the opposite side near
  // an edge, shift along the edge so it never clips. Falls back to a manual clamp
  // if Floating UI didn't load.
  function positionPopover(reference, placement) {
    if (FUI.computePosition) {
      const reposition = () =>
        FUI.computePosition(reference, popover, {
          strategy: 'fixed',
          placement,
          middleware: [FUI.offset(8), FUI.flip({ padding: 8 }), FUI.shift({ padding: 8 })],
        }).then(({ x, y }) => Object.assign(popover.style, { left: `${x}px`, top: `${y}px` }));
      stopAutoPosition = FUI.autoUpdate(reference, popover, reposition);
      return;
    }
    const PAD = 8;
    const a = reference.getBoundingClientRect();
    const pr = popover.getBoundingClientRect();
    const x = Math.max(PAD, Math.min(a.right + PAD, innerWidth - pr.width - PAD));
    const y = Math.max(PAD, Math.min(a.top, innerHeight - pr.height - PAD));
    Object.assign(popover.style, { left: `${x}px`, top: `${y}px` });
  }

  // draft: annotation fields (without label/comment) + an anchor — either
  // `anchorEl` (a real element, tracked on scroll) or `anchorRect` (viewport-space).
  // existing: pass an annotation object to view/edit/delete it instead.
  function openPopover(draft, existing) {
    closePopover();
    if (!existing) {
      showProvisional(draft, draft.kind === 'region');
    } else if (existing.kind === 'region') {
      // Re-open the saved box as an adjustable provisional
      existing.els.forEach((e) => (e.style.display = 'none'));
      editingHidden = existing;
      showProvisional({ kind: 'region', rect: existing.rect }, true);
    }
    popover = el('dialog', 'popover');
    // Native close (Esc / backdrop) tears down our state too. Idempotent with
    // the explicit close in closePopover (guarded on a null popover ref).
    popover.addEventListener('close', () => closePopover());

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
        if (existing.kind === 'region' && provisionalEl) {
          existing.rect = readProvisionalRect();
          editingHidden = null; // visuals rebuilt below; nothing left to unhide
          attachVisuals(existing, 0);
        }
      } else {
        if (draft.kind === 'region' && provisionalEl) draft.rect = readProvisionalRect();
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
    // showModal() promotes it to the top layer as the topmost modal, even nested
    // in our (possibly page-inert) host — that's what frees the textarea to focus.
    try { popover.showModal(); } catch (_) {}
    // Region edits anchor to the live provisional box; element/marker edits to the
    // anchor the caller passed; a note has no page anchor, so float it at center.
    const reference =
      (a.kind === 'region' && provisionalEl) ||
      draft.anchorEl ||
      (draft.anchorRect && { getBoundingClientRect: () => draft.anchorRect }) || {
        getBoundingClientRect: () => {
          const cx = innerWidth / 2, cy = innerHeight / 3;
          return { x: cx, y: cy, top: cy, left: cx, right: cx, bottom: cy, width: 0, height: 0 };
        },
      };
    positionPopover(reference, draft.kind === 'note' ? 'bottom' : 'right-start');
    ta.focus();
    ta.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) save.click();
      if (e.key === 'Escape') closePopover();
      e.stopPropagation();
    });
  }

  // --- Annotation store + markers ---------------------------------------------
  function attachVisuals(a, noteIndex) {
    a.els?.forEach((e) => e.remove());
    a.els = [];
    let mx, my;
    if (a.kind === 'note') {
      mx = scrollX + 10;
      my = scrollY + 10 + noteIndex * 28;
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
      openPopover({ anchorEl: m, kind: a.kind, tag: a.tag, selector: a.selector }, a);
      e.stopPropagation();
    });
    layer.appendChild(m);
    a.els.push(m);
  }

  function addAnnotation(a) {
    a.label = nextLabel++;
    const noteStack = annotations.filter((x) => x.kind === 'note').length;
    attachVisuals(a, noteStack);
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
    hideHost();
  }

  // --- Browser bridge: console buffer + executor commands ----------------------
  const consoleBuf = [];
  window.addEventListener('message', (e) => {
    if (e.source === window && e.data && e.data.__tyConsole) {
      consoleBuf.push(e.data.__tyConsole);
      if (consoleBuf.length > 300) consoleBuf.shift();
    }
  });

  // --- Agent cursor: a visible pointer showing where the executor is acting.
  // The bridge drives the page by selector (no real pointer), so nothing is
  // normally visible when the agent clicks or types. This paints a little
  // labeled cursor that flies to the target and ripples on click, so you can
  // watch what the executor is doing in your tab.
  let agentCursorEl = null;
  let agentCursorHideTimer = null;

  function ensureAgentCursor() {
    if (agentCursorEl && document.documentElement.contains(agentCursorEl)) return agentCursorEl;
    const c = document.createElement('div');
    c.setAttribute('data-ty-agent-cursor', '');
    c.style.cssText =
      'position:fixed;left:0;top:0;z-index:2147483647;pointer-events:none;' +
      'width:0;height:0;opacity:0;' +
      'transition:transform .28s cubic-bezier(.22,.61,.36,1),opacity .2s ease;' +
      'will-change:transform,opacity;';
    c.innerHTML =
      '<svg width="26" height="26" viewBox="0 0 26 26" style="position:absolute;left:-3px;top:-3px;' +
      'filter:drop-shadow(0 1px 2px rgba(0,0,0,.45))">' +
      '<path d="M4 2 L4 20 L9 15 L12.5 22 L15.5 20.5 L12 13.5 L19 13 Z" ' +
      'fill="#d05010" stroke="#fff" stroke-width="1.5" stroke-linejoin="round"/></svg>' +
      '<span style="position:absolute;left:19px;top:13px;white-space:nowrap;' +
      "font:600 11px/1.4 -apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;color:#fff;" +
      'background:#d05010;padding:1px 6px;border-radius:6px;box-shadow:0 1px 3px rgba(0,0,0,.35)">agent</span>';
    document.documentElement.appendChild(c);
    agentCursorEl = c;
    return c;
  }

  function showAgentCursor(x, y) {
    const c = ensureAgentCursor();
    c.style.opacity = '1';
    c.style.transform = `translate(${Math.round(x)}px, ${Math.round(y)}px)`;
    clearTimeout(agentCursorHideTimer);
    agentCursorHideTimer = setTimeout(() => { c.style.opacity = '0'; }, 2600);
  }

  function spawnClickRipple(x, y) {
    const ring = document.createElement('div');
    ring.style.cssText =
      `position:fixed;left:${Math.round(x)}px;top:${Math.round(y)}px;z-index:2147483646;` +
      'pointer-events:none;width:16px;height:16px;margin:-8px 0 0 -8px;' +
      'border:2px solid #d05010;border-radius:50%;opacity:.9;' +
      'transition:transform .5s ease-out,opacity .5s ease-out;';
    document.documentElement.appendChild(ring);
    requestAnimationFrame(() => {
      ring.style.transform = 'scale(3)';
      ring.style.opacity = '0';
    });
    setTimeout(() => ring.remove(), 560);
  }

  // Center of an element in viewport coords (for the fixed-position cursor).
  function elementCenter(el) {
    const r = el.getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  }

  // --- Indexed DOM snapshot ----------------------------------------------------
  // A compact, numbered list of the page's *interactive* elements — the
  // browser-use-style representation. Typically ~20-50x smaller than the raw
  // outerHTML dump, and each element gets a stable index the executor can
  // click/type by, so it never has to guess a selector (and can't ambiguously
  // match two links like the old a[href="/marketplace"] bug). The result reports
  // both its own size and the outerHTML size so you can compare token cost.
  let domIndex = []; // index -> element, rebuilt on each buildDomSnapshot()

  const INTERACTIVE_TAGS = new Set([
    'a', 'button', 'input', 'select', 'textarea', 'summary', 'label', 'option',
  ]);
  const INTERACTIVE_ROLES = new Set([
    'button', 'link', 'checkbox', 'radio', 'tab', 'menuitem', 'menuitemcheckbox',
    'menuitemradio', 'switch', 'option', 'combobox', 'textbox', 'searchbox',
    'slider', 'spinbutton',
  ]);

  function isVisible(el) {
    if (!el || el.nodeType !== 1) return false;
    if (typeof el.checkVisibility === 'function' &&
        !el.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true })) {
      return false;
    }
    const r = el.getBoundingClientRect();
    if (r.width > 0 || r.height > 0) return true;
    // Zero-size but maybe a display:contents wrapper whose children are real —
    // descend into those rather than dropping the subtree. (getComputedStyle is
    // only paid on the rare zero-box element, not every node.)
    return getComputedStyle(el).display === 'contents';
  }

  function isInteractive(el) {
    const tag = el.localName;
    if (INTERACTIVE_TAGS.has(tag)) {
      if (tag === 'input' && el.type === 'hidden') return false;
      return true;
    }
    const role = el.getAttribute('role');
    if (role && INTERACTIVE_ROLES.has(role)) return true;
    if (el.hasAttribute('onclick')) return true;
    if (el.isContentEditable) return true;
    const ti = el.getAttribute('tabindex');
    return ti !== null && ti !== '-1';
  }

  function accessibleName(el) {
    const label = el.getAttribute('aria-label');
    if (label) return label.trim();
    const labelledby = el.getAttribute('aria-labelledby');
    if (labelledby) {
      const names = labelledby
        .split(/\s+/)
        .map((id) => document.getElementById(id)?.innerText || '')
        .join(' ')
        .trim();
      if (names) return names;
    }
    if (el.localName === 'img') return (el.getAttribute('alt') || '').trim();
    if (el.localName === 'input') {
      if (el.type === 'submit' || el.type === 'button') return (el.value || '').trim();
      return (el.getAttribute('placeholder') || el.getAttribute('name') || '').trim();
    }
    const text = (el.innerText || el.textContent || '').replace(/\s+/g, ' ').trim();
    if (text) return text.slice(0, 120);
    return (el.getAttribute('title') || el.getAttribute('name') || '').trim();
  }

  function descriptor(el, idx, depth) {
    const role = el.getAttribute('role') || el.localName;
    const name = accessibleName(el);
    const bits = [];
    if (el.localName === 'input' && el.type) bits.push(`type=${el.type}`);
    const href = el.getAttribute && el.getAttribute('href');
    if (href) bits.push(`href=${href.slice(0, 60)}`);
    if (el.disabled) bits.push('disabled');
    if (el.checked) bits.push('checked');
    const attrs = bits.length ? ` {${bits.join(' ')}}` : '';
    const indent = '  '.repeat(Math.min(depth, 8));
    return `${indent}[${idx}] <${role}>${name ? ` "${name}"` : ''}${attrs}`;
  }

  function walkDom(node, out, depth) {
    for (const el of node.children || []) {
      if (!isVisible(el)) continue;
      let indexed = false;
      if (isInteractive(el)) {
        const idx = domIndex.length;
        domIndex.push(el);
        out.push(descriptor(el, idx, depth));
        indexed = true;
      }
      const childDepth = depth + (indexed ? 1 : 0);
      if (el.shadowRoot) walkDom(el.shadowRoot, out, childDepth + 1);
      walkDom(el, out, childDepth);
      if (el.localName === 'iframe') {
        try {
          const doc = el.contentDocument;
          if (doc && doc.body) walkDom(doc.body, out, childDepth + 1);
        } catch { /* cross-origin frame: not accessible */ }
      }
    }
  }

  function buildDomSnapshot() {
    domIndex = [];
    const out = [];
    walkDom(document.body, out, 0);
    let text = out.join('\n');
    let truncated = false;
    if (text.length > 40_000) {
      text = text.slice(0, 40_000) + '\n… (truncated)';
      truncated = true;
    }
    return {
      dom: text,
      title: document.title,
      url: location.href,
      count: domIndex.length,
      chars: text.length,
      htmlChars: document.documentElement.outerHTML.length, // for token-cost comparison
      truncated,
    };
  }

  // Resolve a click/type target by index (preferred) or CSS selector (fallback).
  function resolveTarget(params) {
    if (params.index != null) {
      const el = domIndex[Number(params.index)];
      if (!el) return { error: `no element at index ${params.index} — take an 'elements' snapshot first` };
      if (!el.isConnected) return { error: `element [${params.index}] is stale — re-snapshot with 'elements'` };
      return { el };
    }
    if (params.selector) {
      const el = document.querySelector(params.selector);
      if (!el) return { error: `no element matches ${params.selector}` };
      return { el };
    }
    return { error: 'click/type needs an "index" (from an elements snapshot) or a "selector"' };
  }

  function runBridgeCommand(action, params) {
    switch (action) {
      case 'elements':
        return buildDomSnapshot();
      case 'snapshot': {
        let html = document.documentElement.outerHTML;
        if (html.length > 800_000) html = html.slice(0, 800_000) + '\n<!-- …truncated -->';
        return { html, title: document.title, url: location.href };
      }
      case 'click': {
        const r = resolveTarget(params);
        if (r.error) return r;
        const el = r.el;
        el.scrollIntoView({ block: 'center', behavior: 'instant' });
        const { x, y } = elementCenter(el);
        showAgentCursor(x, y);
        // Let the cursor visibly land before the ripple fires.
        setTimeout(() => spawnClickRipple(x, y), 260);
        el.click();
        return { ok: true, tag: el.localName, text: (el.innerText || '').trim().slice(0, 80) };
      }
      case 'type': {
        const r = resolveTarget(params);
        if (r.error) return r;
        const el = r.el;
        el.scrollIntoView({ block: 'center', behavior: 'instant' });
        const { x, y } = elementCenter(el);
        showAgentCursor(x, y);
        el.focus();
        if (el.isContentEditable) {
          el.textContent = params.text ?? '';
        } else {
          el.value = params.text ?? '';
        }
        el.dispatchEvent(new Event('input', { bubbles: true }));
        el.dispatchEvent(new Event('change', { bubbles: true }));
        return { ok: true, tag: el.localName };
      }
      case 'console':
        return { logs: consoleBuf.slice(-Number(params.limit || 100)) };
      default:
        return { error: 'unknown action: ' + action };
    }
  }

  // --- Messages from SW / side panel -------------------------------------------
  chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
    switch (msg?.type) {
      case 'ty-cmd':
        try {
          sendResponse(runBridgeCommand(msg.action, msg.params || {}));
        } catch (e) {
          sendResponse({ error: e.message });
        }
        break;
      case 'ty-enter-select':
        showHost();
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
      case 'ty-toast':
        toast(msg.message, 4000);
        sendResponse({ ok: true });
        break;
      default:
        return false;
    }
    return false;
  });

  window.__tyAnnotate = {
    show() {
      showHost();
      setMode('select');
    },
  };
})();
