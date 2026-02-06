// Package widget provides the embeddable JavaScript feedback widget.
package widget

import (
	"fmt"
	"strings"
)

// Generate returns the JavaScript widget code configured for the given API URL.
func Generate(apiURL, project string) string {
	js := widgetJS
	js = strings.ReplaceAll(js, "{{API_URL}}", apiURL)
	js = strings.ReplaceAll(js, "{{PROJECT}}", project)
	return js
}

// ScriptTag returns an HTML script tag to embed the widget.
func ScriptTag(serverURL, apiKey string) string {
	tag := fmt.Sprintf(`<script src="%s/widget.js"`, serverURL)
	if apiKey != "" {
		tag += fmt.Sprintf(` data-api-key="%s"`, apiKey)
	}
	tag += "></script>"
	return tag
}

const widgetJS = `(function() {
  'use strict';

  var TY_API = '{{API_URL}}';
  var TY_PROJECT = '{{PROJECT}}';

  // Read config from script tag
  var scriptTag = document.currentScript;
  var apiKey = scriptTag ? scriptTag.getAttribute('data-api-key') : '';
  var position = scriptTag ? (scriptTag.getAttribute('data-position') || 'bottom-right') : 'bottom-right';

  // State
  var isOpen = false;
  var submitting = false;
  var tasks = [];

  // Styles
  var css = '' +
    '#ty-feedback-btn {' +
    '  position: fixed; z-index: 99999;' +
    '  width: 48px; height: 48px; border-radius: 50%;' +
    '  background: #6366f1; color: white; border: none; cursor: pointer;' +
    '  font-size: 20px; display: flex; align-items: center; justify-content: center;' +
    '  box-shadow: 0 4px 12px rgba(99,102,241,0.4);' +
    '  transition: transform 0.2s, box-shadow 0.2s;' +
    '}' +
    '#ty-feedback-btn:hover { transform: scale(1.1); box-shadow: 0 6px 16px rgba(99,102,241,0.5); }' +
    '#ty-feedback-panel {' +
    '  position: fixed; z-index: 99998;' +
    '  width: 360px; max-height: 500px;' +
    '  background: #1a1a2e; color: #e0e0e0; border-radius: 12px;' +
    '  box-shadow: 0 8px 32px rgba(0,0,0,0.3); font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;' +
    '  font-size: 14px; overflow: hidden; display: none; flex-direction: column;' +
    '}' +
    '#ty-feedback-panel.open { display: flex; }' +
    '#ty-feedback-header {' +
    '  padding: 14px 16px; background: #16213e; border-bottom: 1px solid #2a2a4a;' +
    '  display: flex; justify-content: space-between; align-items: center; flex-shrink: 0;' +
    '}' +
    '#ty-feedback-header h3 { margin: 0; font-size: 14px; font-weight: 600; color: #fff; }' +
    '#ty-feedback-tabs {' +
    '  display: flex; gap: 0; border-bottom: 1px solid #2a2a4a; flex-shrink: 0;' +
    '}' +
    '.ty-tab {' +
    '  flex: 1; padding: 8px; text-align: center; cursor: pointer;' +
    '  background: none; border: none; color: #888; font-size: 12px;' +
    '  border-bottom: 2px solid transparent; transition: all 0.2s;' +
    '}' +
    '.ty-tab:hover { color: #bbb; }' +
    '.ty-tab.active { color: #6366f1; border-bottom-color: #6366f1; }' +
    '#ty-feedback-body { padding: 16px; overflow-y: auto; flex: 1; }' +
    '#ty-feedback-body label { display: block; margin-bottom: 4px; font-size: 12px; color: #999; }' +
    '#ty-feedback-body select,' +
    '#ty-feedback-body input,' +
    '#ty-feedback-body textarea {' +
    '  width: 100%; padding: 8px 10px; margin-bottom: 12px;' +
    '  background: #0f0f23; color: #e0e0e0; border: 1px solid #2a2a4a; border-radius: 6px;' +
    '  font-size: 13px; font-family: inherit; box-sizing: border-box; resize: vertical;' +
    '}' +
    '#ty-feedback-body select:focus,' +
    '#ty-feedback-body input:focus,' +
    '#ty-feedback-body textarea:focus { outline: none; border-color: #6366f1; }' +
    '#ty-submit-btn {' +
    '  width: 100%; padding: 10px; background: #6366f1; color: white;' +
    '  border: none; border-radius: 6px; cursor: pointer; font-size: 13px; font-weight: 500;' +
    '  transition: background 0.2s;' +
    '}' +
    '#ty-submit-btn:hover { background: #4f46e5; }' +
    '#ty-submit-btn:disabled { background: #4a4a6a; cursor: not-allowed; }' +
    '#ty-success { display: none; text-align: center; padding: 20px; }' +
    '#ty-success .check { font-size: 32px; margin-bottom: 8px; }' +
    '#ty-tasks-list { list-style: none; padding: 0; margin: 0; }' +
    '#ty-tasks-list li {' +
    '  padding: 10px; border-bottom: 1px solid #2a2a4a; cursor: pointer;' +
    '}' +
    '#ty-tasks-list li:hover { background: #16213e; }' +
    '.ty-task-status {' +
    '  display: inline-block; padding: 2px 6px; border-radius: 4px;' +
    '  font-size: 10px; font-weight: 600; text-transform: uppercase;' +
    '}' +
    '.ty-status-backlog { background: #2a2a4a; color: #888; }' +
    '.ty-status-queued { background: #1e3a5f; color: #60a5fa; }' +
    '.ty-status-processing { background: #1e3a1e; color: #4ade80; }' +
    '.ty-status-blocked { background: #5f3a1e; color: #fb923c; }' +
    '.ty-status-done { background: #1a2e1a; color: #22c55e; }' +
    '.ty-close-btn {' +
    '  background: none; border: none; color: #888; cursor: pointer; font-size: 18px; padding: 0 4px;' +
    '}' +
    '.ty-close-btn:hover { color: #fff; }' +
    '.ty-empty { text-align: center; color: #666; padding: 20px; font-size: 13px; }';

  // Inject styles
  var style = document.createElement('style');
  style.textContent = css;
  document.head.appendChild(style);

  // Position helper
  function getPositionCSS(pos) {
    switch(pos) {
      case 'bottom-left': return 'bottom: 20px; left: 20px;';
      case 'top-right': return 'top: 20px; right: 20px;';
      case 'top-left': return 'top: 20px; left: 20px;';
      default: return 'bottom: 20px; right: 20px;';
    }
  }

  function getPanelPositionCSS(pos) {
    switch(pos) {
      case 'bottom-left': return 'bottom: 76px; left: 20px;';
      case 'top-right': return 'top: 76px; right: 20px;';
      case 'top-left': return 'top: 76px; left: 20px;';
      default: return 'bottom: 76px; right: 20px;';
    }
  }

  // Create button
  var btn = document.createElement('button');
  btn.id = 'ty-feedback-btn';
  btn.setAttribute('style', getPositionCSS(position));
  btn.innerHTML = '\u2709';
  btn.title = 'Send Feedback';
  document.body.appendChild(btn);

  // Create panel
  var panel = document.createElement('div');
  panel.id = 'ty-feedback-panel';
  panel.setAttribute('style', getPanelPositionCSS(position));
  panel.innerHTML = '' +
    '<div id="ty-feedback-header">' +
    '  <h3>Feedback \u2014 ' + escapeHtml(TY_PROJECT) + '</h3>' +
    '  <button class="ty-close-btn" id="ty-close">\u00d7</button>' +
    '</div>' +
    '<div id="ty-feedback-tabs">' +
    '  <button class="ty-tab active" data-tab="submit">Submit</button>' +
    '  <button class="ty-tab" data-tab="tasks">Tasks</button>' +
    '</div>' +
    '<div id="ty-feedback-body">' +
    '  <div id="ty-tab-submit">' +
    '    <div id="ty-form">' +
    '      <label>Category</label>' +
    '      <select id="ty-category">' +
    '        <option value="bug">Bug Report</option>' +
    '        <option value="feature">Feature Request</option>' +
    '        <option value="question">Question</option>' +
    '        <option value="other">Other</option>' +
    '      </select>' +
    '      <label>Title (optional)</label>' +
    '      <input type="text" id="ty-title" placeholder="Brief summary...">' +
    '      <label>Description</label>' +
    '      <textarea id="ty-body" rows="4" placeholder="What happened? What did you expect?"></textarea>' +
    '      <button id="ty-submit-btn">Submit Feedback</button>' +
    '    </div>' +
    '    <div id="ty-success">' +
    '      <div class="check">\u2705</div>' +
    '      <div>Feedback submitted!</div>' +
    '      <div style="color:#888;font-size:12px;margin-top:4px">Task <span id="ty-task-id"></span> created</div>' +
    '      <button id="ty-another-btn" style="margin-top:12px;padding:6px 16px;background:#2a2a4a;color:#e0e0e0;border:none;border-radius:6px;cursor:pointer;">Submit Another</button>' +
    '    </div>' +
    '  </div>' +
    '  <div id="ty-tab-tasks" style="display:none">' +
    '    <ul id="ty-tasks-list"></ul>' +
    '  </div>' +
    '</div>';
  document.body.appendChild(panel);

  // Event handlers
  btn.addEventListener('click', function() {
    isOpen = !isOpen;
    panel.classList.toggle('open', isOpen);
    if (isOpen) {
      btn.innerHTML = '\u2715';
    } else {
      btn.innerHTML = '\u2709';
    }
  });

  document.getElementById('ty-close').addEventListener('click', function() {
    isOpen = false;
    panel.classList.remove('open');
    btn.innerHTML = '\u2709';
  });

  // Tabs
  var tabs = panel.querySelectorAll('.ty-tab');
  tabs.forEach(function(tab) {
    tab.addEventListener('click', function() {
      var target = this.getAttribute('data-tab');
      tabs.forEach(function(t) { t.classList.remove('active'); });
      this.classList.add('active');
      document.getElementById('ty-tab-submit').style.display = target === 'submit' ? 'block' : 'none';
      document.getElementById('ty-tab-tasks').style.display = target === 'tasks' ? 'block' : 'none';
      if (target === 'tasks') loadTasks();
    });
  });

  // Submit
  document.getElementById('ty-submit-btn').addEventListener('click', function() {
    if (submitting) return;
    var body = document.getElementById('ty-body').value.trim();
    if (!body) {
      document.getElementById('ty-body').style.borderColor = '#ef4444';
      return;
    }
    document.getElementById('ty-body').style.borderColor = '#2a2a4a';

    submitting = true;
    this.disabled = true;
    this.textContent = 'Submitting...';

    apiRequest('POST', '/api/feedback', {
      title: document.getElementById('ty-title').value.trim(),
      body: body,
      category: document.getElementById('ty-category').value,
      url: window.location.href,
      user: ''
    }, function(data) {
      submitting = false;
      document.getElementById('ty-submit-btn').disabled = false;
      document.getElementById('ty-submit-btn').textContent = 'Submit Feedback';
      document.getElementById('ty-form').style.display = 'none';
      document.getElementById('ty-success').style.display = 'block';
      document.getElementById('ty-task-id').textContent = '#' + data.id;
    }, function(err) {
      submitting = false;
      document.getElementById('ty-submit-btn').disabled = false;
      document.getElementById('ty-submit-btn').textContent = 'Submit Feedback';
      alert('Failed to submit: ' + err);
    });
  });

  // Reset form
  document.getElementById('ty-another-btn').addEventListener('click', function() {
    document.getElementById('ty-form').style.display = 'block';
    document.getElementById('ty-success').style.display = 'none';
    document.getElementById('ty-title').value = '';
    document.getElementById('ty-body').value = '';
  });

  // Load tasks
  function loadTasks() {
    var list = document.getElementById('ty-tasks-list');
    list.innerHTML = '<li class="ty-empty">Loading...</li>';

    apiRequest('GET', '/api/tasks', null, function(data) {
      if (!data || data.length === 0) {
        list.innerHTML = '<li class="ty-empty">No tasks yet</li>';
        return;
      }
      tasks = data;
      list.innerHTML = '';
      data.forEach(function(task) {
        var li = document.createElement('li');
        li.innerHTML = '<div style="display:flex;justify-content:space-between;align-items:center">' +
          '<span style="font-weight:500">#' + task.id + '</span>' +
          '<span class="ty-task-status ty-status-' + task.status + '">' + task.status + '</span>' +
          '</div>' +
          '<div style="margin-top:4px;font-size:13px">' + escapeHtml(task.title) + '</div>';
        list.appendChild(li);
      });
    }, function() {
      list.innerHTML = '<li class="ty-empty">Failed to load tasks</li>';
    });
  }

  // API helper
  function apiRequest(method, path, body, onSuccess, onError) {
    var xhr = new XMLHttpRequest();
    xhr.open(method, TY_API + path);
    xhr.setRequestHeader('Content-Type', 'application/json');
    if (apiKey) {
      xhr.setRequestHeader('Authorization', 'Bearer ' + apiKey);
    }
    xhr.onload = function() {
      if (xhr.status >= 200 && xhr.status < 300) {
        try { onSuccess(JSON.parse(xhr.responseText)); }
        catch(e) { onSuccess(null); }
      } else {
        try { onError(JSON.parse(xhr.responseText).error); }
        catch(e) { onError('Request failed (' + xhr.status + ')'); }
      }
    };
    xhr.onerror = function() { onError('Network error'); };
    xhr.send(body ? JSON.stringify(body) : null);
  }

  function escapeHtml(s) {
    var div = document.createElement('div');
    div.textContent = s;
    return div.innerHTML;
  }
})();`
