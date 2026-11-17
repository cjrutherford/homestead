#!/usr/bin/env python3
"""Generate docs/reviews/acceptance-form.html from the owner acceptance checklist.

Stdlib only; run with `python3 -I scripts/review/make_acceptance_form.py`.
`--check` exits 1 when the committed HTML is not up to date.

The output is one self-contained file: inline CSS and JS, no external
resources, no network. The checklist is embedded as JSON; the page builds its
DOM with textContent only.
"""
import hashlib
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent.parent
CHECKLIST = ROOT / "docs" / "reviews" / "OWNER_ACCEPTANCE_CHECKLIST.md"
OUTPUT = ROOT / "docs" / "reviews" / "acceptance-form.html"

STEP_RE = re.compile(r"^- \[[ xX]\] \*\*(\S+) — (.+?)\*\*\s*$")
FIELD_RE = re.compile(r"^  - (Action|Expected|If it fails|Result|Note): ?(.*)$")


def parse(md):
    """Return the checklist structure: sections > groups > steps."""
    sections = []
    section = None
    group = None
    step = None
    field = None

    def new_group(title):
        nonlocal group
        group = {"title": title, "note": "", "steps": []}
        section["groups"].append(group)

    for line in md.split("\n"):
        if line.startswith("## "):
            section = {"title": line[3:].strip(), "intro": "", "groups": []}
            sections.append(section)
            group = None
            step = None
            field = None
            continue
        if section is None:
            continue
        if line.startswith("### "):
            new_group(line[4:].strip())
            step = None
            field = None
            continue
        m = STEP_RE.match(line)
        if m:
            if group is None:
                new_group(section["title"])
            step = {"id": m.group(1), "title": m.group(2), "action": "", "expected": "", "fail": ""}
            group["steps"].append(step)
            field = None
            continue
        m = FIELD_RE.match(line)
        if m and step is not None:
            key = {"Action": "action", "Expected": "expected", "If it fails": "fail"}.get(m.group(1))
            field = key
            if key:
                step[key] = m.group(2).strip()
            continue
        if step is not None and field and line.startswith("    "):
            step[field] += " " + line.strip()
            continue
        if line.strip() == "":
            continue
        # prose between headings and the first step
        if step is None:
            text = line.strip()
            if text.startswith("_") and text.endswith("_") and group is not None:
                group["note"] = (group["note"] + " " + text.strip("_")).strip()
            elif group is None:
                section["intro"] = (section["intro"] + " " + text).strip()
            elif not group["steps"]:
                group["note"] = (group["note"] + " " + text).strip()
    out = []
    for s in sections:
        s["groups"] = [g for g in s["groups"] if g["steps"]]
        if s["groups"]:
            out.append(s)
    return out


def digest(md):
    return hashlib.sha256(md.encode("utf-8")).hexdigest()[:16]


def step_ids(sections):
    return [st["id"] for s in sections for g in s["groups"] for st in g["steps"]]


def render(md):
    sections = parse(md)
    data = {"hash": digest(md), "sections": sections}
    blob = json.dumps(data, ensure_ascii=False, indent=1)
    # keep the JSON inert inside <script>
    blob = blob.replace("<", "\\u003c").replace(">", "\\u003e").replace("&", "\\u0026")
    return TEMPLATE.replace("/*__DATA__*/", blob)


def main(argv):
    md = CHECKLIST.read_text(encoding="utf-8")
    html = render(md)
    if "--check" in argv:
        if not OUTPUT.exists() or OUTPUT.read_text(encoding="utf-8") != html:
            print("acceptance-form.html is out of date; run python3 -I scripts/review/make_acceptance_form.py")
            return 1
        print("acceptance-form.html is up to date")
        return 0
    OUTPUT.write_text(html, encoding="utf-8")
    n = len(step_ids(parse(md)))
    print("wrote %s (%d steps)" % (OUTPUT.relative_to(ROOT), n))
    return 0


TEMPLATE = r"""<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Homestead acceptance form</title>
<style>
:root{--bg:#f6f7f9;--fg:#1c2128;--mut:#5b6573;--line:#d5dae1;--card:#fff;--acc:#1f5fbf;--pass:#1a7f37;--fail:#c62828;--blk:#b45309;--skip:#6b7280}
*{box-sizing:border-box}
body{margin:0;font:15px/1.45 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;background:var(--bg);color:var(--fg)}
header.top{position:sticky;top:0;z-index:10;background:#fff;border-bottom:1px solid var(--line);padding:8px 12px}
header.top h1{font-size:16px;margin:0 12px 0 0;display:inline-block}
.bar{display:flex;flex-wrap:wrap;gap:8px;align-items:center}
.progress{flex:1 1 220px;min-width:160px}
.meter{height:10px;background:#e4e8ee;border-radius:5px;overflow:hidden;display:flex}
.meter span{display:block;height:100%}
.m-pass{background:var(--pass)}.m-fail{background:var(--fail)}.m-blk{background:var(--blk)}.m-skip{background:var(--skip)}
.stat{font-size:12px;color:var(--mut)}
button,select,input,textarea{font:inherit}
button{border:1px solid var(--line);background:#fff;border-radius:6px;padding:5px 10px;cursor:pointer}
button.primary{background:var(--acc);border-color:var(--acc);color:#fff}
button.danger{color:var(--fail)}
button:focus-visible,input:focus-visible,select:focus-visible,textarea:focus-visible{outline:2px solid var(--acc);outline-offset:1px}
details.runinfo{margin-top:6px}
details.runinfo summary{cursor:pointer;font-size:13px;color:var(--mut)}
.runinfo .grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:8px;margin-top:6px}
label.f{display:flex;flex-direction:column;font-size:12px;color:var(--mut);gap:2px}
label.f input,label.f select,label.f textarea{border:1px solid var(--line);border-radius:6px;padding:5px 7px;color:var(--fg);background:#fff;width:100%}
label.f textarea{min-height:60px;resize:vertical}
.layout{display:flex;align-items:flex-start}
nav.side{width:280px;flex:0 0 280px;position:sticky;top:var(--topH,110px);max-height:calc(100vh - var(--topH,110px));overflow:auto;padding:10px;border-right:1px solid var(--line);background:#fff}
nav.side h2{font-size:12px;text-transform:uppercase;letter-spacing:.04em;color:var(--mut);margin:12px 0 4px}
nav.side a{display:flex;justify-content:space-between;gap:6px;padding:3px 6px;border-radius:5px;color:var(--fg);text-decoration:none;font-size:13px}
nav.side a:hover{background:#eef2f8}
nav.side a .cnt{color:var(--mut);white-space:nowrap}
nav.side a .cnt b{color:var(--fail)}
nav.side a.done .cnt{color:var(--pass)}
main{flex:1;min-width:0;padding:12px}
.tools{display:flex;flex-wrap:wrap;gap:8px;align-items:center;margin-bottom:10px}
.tools input[type=search]{flex:1 1 220px;border:1px solid var(--line);border-radius:6px;padding:6px 8px}
.seg button.on{background:var(--acc);color:#fff;border-color:var(--acc)}
section.sec>h2{font-size:20px;margin:22px 0 4px}
.intro,.gnote{color:var(--mut);font-size:13px;margin:2px 0 8px}
section.grp>h3{font-size:16px;margin:16px 0 6px;scroll-margin-top:var(--topH,110px)}
article.step{background:var(--card);border:1px solid var(--line);border-left:5px solid var(--line);border-radius:8px;padding:10px 12px;margin:8px 0}
article.step.r-pass{border-left-color:var(--pass)}
article.step.r-fail{border-left-color:var(--fail)}
article.step.r-blocked{border-left-color:var(--blk)}
article.step.r-skip{border-left-color:var(--skip)}
.sid{font:600 12px ui-monospace,Menlo,Consolas,monospace;color:var(--acc)}
.stitle{font-weight:600;margin-left:6px}
dl.info{margin:6px 0;display:grid;grid-template-columns:90px 1fr;gap:3px 8px;font-size:14px}
dl.info dt{color:var(--mut);font-size:12px;text-transform:uppercase;letter-spacing:.03em;padding-top:2px}
dl.info dd{margin:0;overflow-wrap:anywhere}
dl.info code{font-family:ui-monospace,monospace;font-size:.92em;background:#f1f3f5;border-radius:3px;padding:0 3px}
.row{display:flex;flex-wrap:wrap;gap:8px;align-items:flex-end;margin-top:6px}
.row label.f{flex:1 1 200px}
.row label.f.res{flex:0 0 140px}
.warn{background:#fff7e0;border:1px solid #ecd48a;color:#6b4e00;border-radius:6px;padding:4px 8px;font-size:12px;margin-top:6px}
.issue{border:1px solid #e3b5b5;background:#fff8f8;border-radius:8px;padding:8px 10px;margin-top:8px}
.issue h4{margin:0 0 6px;font-size:13px;display:flex;justify-content:space-between;align-items:center}
.issue .grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:8px}
.issue .wide{grid-column:1/-1}
.empty{color:var(--mut);padding:20px;text-align:center}
.banner{background:#fff7e0;border:1px solid #ecd48a;border-radius:6px;padding:8px 10px;margin-bottom:10px;font-size:13px}
.hidden{display:none!important}
#menuBtn{display:none}
@media (max-width:820px){
 nav.side{position:fixed;left:0;top:0;bottom:0;max-height:none;z-index:20;width:84%;transform:translateX(-102%);transition:transform .15s}
 body.menu nav.side{transform:none;box-shadow:0 0 0 100vmax rgba(0,0,0,.35)}
 #menuBtn{display:inline-block}
 dl.info{grid-template-columns:1fr}
 .row label.f.res{flex:1 1 100%}
}
</style>
</head>
<body>
<header class="top" id="top">
 <div class="bar">
  <button id="menuBtn" type="button" aria-label="Show navigation">Menu</button>
  <h1>Homestead acceptance form</h1>
  <div class="progress"><div class="meter" id="meter" role="img" aria-label="Overall progress"></div><div class="stat" id="overall"></div></div>
  <button id="exportBtn" class="primary" type="button">Export results</button>
  <button id="importBtn" type="button">Import JSON</button>
  <button id="clearBtn" class="danger" type="button">Clear</button>
  <input id="importFile" type="file" accept="application/json,.json" class="hidden">
 </div>
 <details class="runinfo" open>
  <summary>Run info</summary>
  <div class="grid" id="runinfo"></div>
  <div class="warn">Do not enter passwords, tokens or secrets anywhere in this form. Exports contain only what you type here.</div>
 </details>
</header>
<div class="layout">
 <nav class="side" id="side" aria-label="Checklist navigation"></nav>
 <main id="main">
  <div id="banner"></div>
  <div class="tools">
   <input type="search" id="search" placeholder="Search by ID or text" aria-label="Search steps">
   <span class="seg" id="filters"></span>
  </div>
  <div id="content"></div>
 </main>
</div>
<script id="checklist-data" type="application/json">/*__DATA__*/</script>
<script>
(function () {
'use strict';
var DATA = JSON.parse(document.getElementById('checklist-data').textContent);
var KEY = 'homestead-acceptance-form:' + DATA.hash;
var RESULTS = [['notrun', 'Not run'], ['pass', 'PASS'], ['fail', 'FAIL'], ['skip', 'SKIP'], ['blocked', 'BLOCKED']];
var SEVERITIES = ['Blocker', 'Major', 'Minor', 'Cosmetic'];
var RUNFIELDS = [['tester', 'Tester name'], ['date', 'Date'], ['commit', 'Appliance commit'], ['browser', 'Browser']];
var ISSUEFIELDS = [
  ['actual', 'What happened (actual)', 'ta', true],
  ['expected', 'What you expected', 'ta', true],
  ['repro', 'Steps to reproduce', 'ta', true],
  ['url', 'URL', 'in', false],
  ['screenshots', 'Screenshot file name(s)', 'in', false],
  ['error', 'Error text / log excerpt (no secrets)', 'ta', true],
  ['device', 'Browser / device', 'in', false],
  ['workaround', 'Workaround used', 'in', false]
];

var steps = [];
var stepById = {};
DATA.sections.forEach(function (sec) {
  sec.groups.forEach(function (g) {
    g.steps.forEach(function (s) { s.group = g; s.section = sec; steps.push(s); stepById[s.id] = s; });
  });
});

var state = { version: 1, hash: DATA.hash, run: {}, steps: {} };
var ui = { filter: 'all', query: '' };
var els = {};     // step id -> {root, issuesBox, ...}
var groupEls = [];

function load() {
  try {
    var raw = localStorage.getItem(KEY);
    if (raw) {
      var o = JSON.parse(raw);
      if (o && typeof o === 'object') { state.run = o.run || {}; state.steps = o.steps || {}; }
    }
  } catch (e) { /* storage unavailable: run without persistence */ }
}
var saveTimer = null;
function save() {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(saveNow, 150);
}
function saveNow() {
  try { localStorage.setItem(KEY, JSON.stringify({ version: 1, hash: DATA.hash, run: state.run, steps: state.steps })); } catch (e) { /* ignore */ }
}
function st(id) {
  if (!state.steps[id]) state.steps[id] = { result: 'notrun', notes: '', issues: [] };
  return state.steps[id];
}
function peek(id) { return state.steps[id] || { result: 'notrun', notes: '', issues: [] }; }

function el(tag, attrs, kids) {
  var e = document.createElement(tag);
  if (attrs) Object.keys(attrs).forEach(function (k) {
    if (k === 'text') e.textContent = attrs[k];
    else if (k === 'class') e.className = attrs[k];
    else e.setAttribute(k, attrs[k]);
  });
  (kids || []).forEach(function (c) { if (c) e.appendChild(c); });
  return e;
}
// inline turns `code` and **bold** spans into text nodes inside code/strong
// elements; everything stays textContent, nothing is parsed as HTML.
function inline(text) {
  var out = [], re = /`([^`]+)`|\*\*([^*]+)\*\*/g, last = 0, m;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) out.push(document.createTextNode(text.slice(last, m.index)));
    out.push(m[1] !== undefined ? el('code', { text: m[1] }) : el('strong', { text: m[2] }));
    last = re.lastIndex;
  }
  if (last < text.length) out.push(document.createTextNode(text.slice(last)));
  return out;
}
function labelled(text, control, cls) {
  var l = el('label', { class: 'f' + (cls ? ' ' + cls : '') });
  l.appendChild(el('span', { text: text }));
  l.appendChild(control);
  return l;
}
function slug(s) { return s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, ''); }

/* ---------- run info ---------- */
function buildRunInfo() {
  var box = document.getElementById('runinfo');
  RUNFIELDS.forEach(function (f) {
    var inp = el('input', { type: f[0] === 'date' ? 'date' : 'text', autocomplete: 'off' });
    if (f[0] === 'browser') inp.setAttribute('placeholder', 'e.g. Chrome 130 on Linux');
    if (f[0] === 'commit') inp.setAttribute('placeholder', 'git short hash from PLAT-DEPLOY-1');
    inp.value = state.run[f[0]] || '';
    inp.addEventListener('input', function () { state.run[f[0]] = inp.value; save(); });
    inp.setAttribute('data-run', f[0]);
    box.appendChild(labelled(f[1], inp));
  });
}

/* ---------- build content ---------- */
function resultClass(r) { return r === 'notrun' ? '' : ' r-' + r; }

function buildIssue(s, issue, idx) {
  var box = el('div', { class: 'issue' });
  var h = el('h4');
  h.appendChild(el('span', { text: 'Issue ' + (idx + 1) + ' on ' + s.id }));
  var rm = el('button', { type: 'button', class: 'danger', text: 'Remove issue' });
  rm.addEventListener('click', function () {
    var rec = st(s.id);
    if (!window.confirm('Remove this issue?')) return;
    rec.issues.splice(rec.issues.indexOf(issue), 1);
    save(); refreshStep(s.id); refreshAll();
  });
  h.appendChild(rm);
  box.appendChild(h);
  var grid = el('div', { class: 'grid' });
  var sev = el('select');
  sev.appendChild(el('option', { value: '', text: 'Choose severity' }));
  SEVERITIES.forEach(function (v) { sev.appendChild(el('option', { value: v, text: v })); });
  sev.value = issue.severity || '';
  sev.setAttribute('data-f', 'severity');
  sev.addEventListener('change', function () { issue.severity = sev.value; save(); });
  grid.appendChild(labelled('Severity', sev));
  var can = el('select');
  [['', 'Choose'], ['yes', 'Yes'], ['no', 'No']].forEach(function (o) { can.appendChild(el('option', { value: o[0], text: o[1] })); });
  can.value = issue.canContinue || '';
  can.setAttribute('data-f', 'canContinue');
  can.addEventListener('change', function () { issue.canContinue = can.value; save(); });
  grid.appendChild(labelled('Can you continue?', can));
  ISSUEFIELDS.forEach(function (f) {
    var c = f[2] === 'ta' ? el('textarea') : el('input', { type: 'text', autocomplete: 'off' });
    c.value = issue[f[0]] || '';
    c.setAttribute('data-f', f[0]);
    c.addEventListener('input', function () { issue[f[0]] = c.value; save(); });
    grid.appendChild(labelled(f[1], c, f[3] ? 'wide' : ''));
  });
  box.appendChild(grid);
  return box;
}

function newIssue(s) {
  return { severity: '', actual: '', expected: s.expected, repro: '', url: '', screenshots: '', error: '', device: '', workaround: '', canContinue: '' };
}

function refreshStep(id) {
  var e = els[id];
  var rec = peek(id);
  e.root.className = 'step' + resultClass(rec.result);
  e.root.setAttribute('class', 'step' + resultClass(rec.result));
  e.sel.value = rec.result;
  var need = rec.result === 'fail' || rec.result === 'blocked' || rec.issues.length > 0;
  e.issuesBox.textContent = '';
  if (need) {
    var r = st(id);
    if (!r.issues.length) r.issues.push(newIssue(stepById[id]));
    r.issues.forEach(function (iss, i) { e.issuesBox.appendChild(buildIssue(stepById[id], iss, i)); });
    var add = el('button', { type: 'button', text: 'Add another issue' });
    add.addEventListener('click', function () { st(id).issues.push(newIssue(stepById[id])); save(); refreshStep(id); refreshAll(); });
    e.issuesBox.appendChild(add);
  }
  e.report.classList.toggle('hidden', need);
}

function buildStep(s) {
  var root = el('article', { class: 'step', id: 'step-' + s.id });
  var head = el('div');
  head.appendChild(el('span', { class: 'sid', text: s.id }));
  head.appendChild(el('span', { class: 'stitle', text: s.title }));
  root.appendChild(head);
  var dl = el('dl', { class: 'info' });
  [['Action', s.action], ['Expected', s.expected], ['Where to look', s.fail]].forEach(function (p) {
    if (!p[1]) return;
    dl.appendChild(el('dt', { text: p[0] }));
    dl.appendChild(el('dd', null, inline(p[1])));
  });
  root.appendChild(dl);
  var row = el('div', { class: 'row' });
  var sel = el('select');
  RESULTS.forEach(function (r) { sel.appendChild(el('option', { value: r[0], text: r[1] })); });
  sel.setAttribute('data-f', 'result');
  sel.addEventListener('change', function () {
    var rec = st(s.id);
    rec.result = sel.value;
    save(); refreshStep(s.id); refreshAll();
  });
  row.appendChild(labelled('Result', sel, 'res'));
  var notes = el('textarea', { rows: '2' });
  notes.value = peek(s.id).notes || '';
  notes.setAttribute('data-f', 'notes');
  notes.addEventListener('input', function () { st(s.id).notes = notes.value; save(); updateFilterVisibility(); });
  row.appendChild(labelled('Notes (do not enter passwords, tokens or secrets)', notes));
  var report = el('button', { type: 'button', text: 'Report issue' });
  report.setAttribute('data-act', 'report');
  report.addEventListener('click', function () {
    var rec = st(s.id);
    if (!rec.issues.length) rec.issues.push(newIssue(s));
    save(); refreshStep(s.id); refreshAll();
  });
  row.appendChild(report);
  root.appendChild(row);
  var box = el('div');
  root.appendChild(box);
  els[s.id] = { root: root, sel: sel, notes: notes, issuesBox: box, report: report, search: (s.id + ' ' + s.title + ' ' + s.action + ' ' + s.expected + ' ' + s.fail).toLowerCase() };
  return root;
}

function buildContent() {
  var c = document.getElementById('content');
  DATA.sections.forEach(function (sec) {
    var se = el('section', { class: 'sec', id: 'sec-' + slug(sec.title) });
    se.appendChild(el('h2', { text: sec.title }));
    if (sec.intro) se.appendChild(el('p', { class: 'intro', text: sec.intro }));
    sec.groups.forEach(function (g) {
      var ge = el('section', { class: 'grp', id: 'grp-' + slug(sec.title + '-' + g.title) });
      if (g.title !== sec.title) ge.appendChild(el('h3', { text: g.title }));
      if (g.note) ge.appendChild(el('p', { class: 'gnote', text: g.note }));
      g.steps.forEach(function (s) { ge.appendChild(buildStep(s)); });
      se.appendChild(ge);
      g.el = ge;
      groupEls.push(g);
    });
    c.appendChild(se);
  });
  c.appendChild(el('p', { class: 'empty hidden', id: 'none', text: 'No steps match this filter.' }));
}

/* ---------- navigation ---------- */
var navCounts = [];
function buildNav() {
  var side = document.getElementById('side');
  side.textContent = '';
  navCounts = [];
  DATA.sections.forEach(function (sec) {
    var h = el('h2', { text: sec.title });
    side.appendChild(h);
    sec.groups.forEach(function (g) {
      var a = el('a', { href: '#grp-' + slug(sec.title + '-' + g.title) });
      a.appendChild(el('span', { text: g.title }));
      var cnt = el('span', { class: 'cnt' });
      a.appendChild(cnt);
      a.addEventListener('click', function () { document.body.classList.remove('menu'); });
      side.appendChild(a);
      navCounts.push({ g: g, a: a, cnt: cnt });
    });
  });
}

function tally(list) {
  var t = { total: list.length, pass: 0, fail: 0, skip: 0, blocked: 0, notrun: 0, issues: 0 };
  list.forEach(function (s) {
    var r = peek(s.id);
    t[r.result] = (t[r.result] || 0) + 1;
    t.issues += r.issues.length;
  });
  return t;
}

function refreshNav() {
  navCounts.forEach(function (n) {
    var t = tally(n.g.steps);
    var done = t.total - t.notrun;
    n.cnt.textContent = '';
    n.cnt.appendChild(document.createTextNode(done + '/' + t.total));
    if (t.fail || t.blocked) {
      var b = el('b', { text: ' ' + (t.fail ? t.fail + ' fail' : '') + (t.fail && t.blocked ? ', ' : (t.blocked ? ' ' : '')) + (t.blocked ? t.blocked + ' blocked' : '') });
      n.cnt.appendChild(b);
    }
    n.a.classList.toggle('done', done === t.total);
  });
}

function refreshHeader() {
  var t = tally(steps);
  var done = t.total - t.notrun;
  var m = document.getElementById('meter');
  m.textContent = '';
  [['pass', 'm-pass'], ['fail', 'm-fail'], ['blocked', 'm-blk'], ['skip', 'm-skip']].forEach(function (p) {
    var sp = el('span', { class: p[1] });
    sp.style.width = (t.total ? (100 * t[p[0]] / t.total) : 0) + '%';
    m.appendChild(sp);
  });
  document.getElementById('overall').textContent = done + ' of ' + t.total + ' done (' + (t.total ? Math.round(100 * done / t.total) : 0) + '%): ' +
    t.pass + ' pass, ' + t.fail + ' fail, ' + t.blocked + ' blocked, ' + t.skip + ' skip, ' + t.issues + ' issue' + (t.issues === 1 ? '' : 's');
  var top = document.getElementById('top');
  document.documentElement.style.setProperty('--topH', top.offsetHeight + 'px');
}

function matches(s) {
  var rec = peek(s.id);
  if (ui.filter === 'notrun' && rec.result !== 'notrun') return false;
  if (ui.filter === 'failed' && rec.result !== 'fail' && rec.result !== 'blocked') return false;
  if (ui.filter === 'issues' && !rec.issues.length) return false;
  if (ui.query) {
    var hay = els[s.id].search + ' ' + (rec.notes || '').toLowerCase();
    var q = ui.query.toLowerCase();
    if (hay.indexOf(q) < 0) return false;
  }
  return true;
}

function updateFilterVisibility() {
  var any = false;
  groupEls.forEach(function (g) {
    var shown = 0;
    g.steps.forEach(function (s) {
      var ok = matches(s);
      els[s.id].root.classList.toggle('hidden', !ok);
      if (ok) shown++;
    });
    g.el.classList.toggle('hidden', shown === 0);
    if (shown) any = true;
  });
  document.getElementById('none').classList.toggle('hidden', any);
}

function refreshAll() {
  refreshNav();
  refreshHeader();
  // a changed result can change filter membership; re-evaluate lazily
  if (ui.filter !== 'all') updateFilterVisibility();
}

function buildTools() {
  var f = document.getElementById('filters');
  f.classList.add('seg');
  [['all', 'All'], ['notrun', 'Not run'], ['failed', 'Failed or blocked'], ['issues', 'Has issues']].forEach(function (p) {
    var b = el('button', { type: 'button', text: p[1] });
    b.setAttribute('data-filter', p[0]);
    if (p[0] === ui.filter) b.classList.add('on');
    b.addEventListener('click', function () {
      ui.filter = p[0];
      Array.prototype.forEach.call(f.children, function (x) { x.classList.toggle('on', x === b); });
      updateFilterVisibility();
    });
    f.appendChild(b);
  });
  document.getElementById('search').addEventListener('input', function (ev) {
    ui.query = ev.target.value.trim();
    updateFilterVisibility();
  });
}

/* ---------- export / import ---------- */
function dateStamp() {
  var d = new Date();
  function p(n) { return (n < 10 ? '0' : '') + n; }
  return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate());
}
function oneLine(s) { return String(s || '').replace(/\r?\n/g, ' ').replace(/\|/g, '\\|'); }
function block(s) {
  s = String(s || '').replace(/\s+$/, '');
  if (!s) return '(not given)';
  return s;
}
var RESULT_LABEL = { notrun: 'Not run', pass: 'PASS', fail: 'FAIL', skip: 'SKIP', blocked: 'BLOCKED' };

function buildMarkdown() {
  var L = [];
  var run = state.run;
  L.push('# Acceptance results');
  L.push('');
  L.push('Checklist hash: ' + DATA.hash);
  L.push('Exported: ' + dateStamp());
  L.push('');
  L.push('## Run info');
  L.push('');
  RUNFIELDS.forEach(function (f) { L.push('- ' + f[1] + ': ' + (run[f[0]] ? oneLine(run[f[0]]) : '(not given)')); });
  var all = tally(steps);
  L.push('');
  L.push('## Summary');
  L.push('');
  L.push('Overall: ' + (all.total - all.notrun) + ' of ' + all.total + ' run: ' + all.pass + ' pass, ' + all.fail + ' fail, ' + all.blocked + ' blocked, ' + all.skip + ' skip, ' + all.notrun + ' not run, ' + all.issues + ' issue(s).');
  L.push('');
  L.push('| App / group | Pass | Fail | Skip | Blocked | Not run |');
  L.push('|---|---|---|---|---|---|');
  var issuesAll = [];
  DATA.sections.forEach(function (sec) {
    sec.groups.forEach(function (g) {
      var t = tally(g.steps);
      var name = g.title === sec.title ? g.title : sec.title + ': ' + g.title;
      L.push('| ' + oneLine(name) + ' | ' + t.pass + ' | ' + t.fail + ' | ' + t.skip + ' | ' + t.blocked + ' | ' + t.notrun + ' |');
      g.steps.forEach(function (s) {
        peek(s.id).issues.forEach(function (iss, i) { issuesAll.push({ step: s, issue: iss, n: i + 1 }); });
      });
    });
  });
  L.push('');
  L.push('## Issues (' + issuesAll.length + ')');
  var order = SEVERITIES.concat(['']);
  order.forEach(function (sev) {
    var list = issuesAll.filter(function (x) { return (x.issue.severity || '') === sev; });
    if (!list.length) return;
    L.push('');
    L.push('### ' + (sev || 'Severity not set') + ' (' + list.length + ')');
    list.forEach(function (x) {
      var iss = x.issue;
      L.push('');
      L.push('#### ' + x.step.id + ' issue ' + x.n + ': ' + oneLine(x.step.title));
      L.push('');
      L.push('- Step result: ' + RESULT_LABEL[peek(x.step.id).result]);
      L.push('- Severity: ' + (iss.severity || '(not set)'));
      L.push('- Can continue: ' + (iss.canContinue || '(not given)'));
      L.push('- URL: ' + (iss.url ? oneLine(iss.url) : '(not given)'));
      L.push('- Screenshot(s): ' + (iss.screenshots ? oneLine(iss.screenshots) : '(none)'));
      L.push('- Browser / device: ' + (iss.device ? oneLine(iss.device) : '(not given)'));
      L.push('- Workaround: ' + (iss.workaround ? oneLine(iss.workaround) : '(none)'));
      L.push('');
      L.push('What happened:');
      L.push('');
      L.push(block(iss.actual));
      L.push('');
      L.push('What was expected:');
      L.push('');
      L.push(block(iss.expected));
      L.push('');
      L.push('Steps to reproduce:');
      L.push('');
      L.push(block(iss.repro));
      L.push('');
      L.push('Error text / log excerpt:');
      L.push('');
      L.push(iss.error && iss.error.trim() ? '```\n' + iss.error.replace(/\s+$/, '') + '\n```' : '(none)');
    });
  });
  L.push('');
  L.push('## Per-step results');
  DATA.sections.forEach(function (sec) {
    sec.groups.forEach(function (g) {
      L.push('');
      L.push('### ' + (g.title === sec.title ? g.title : sec.title + ': ' + g.title));
      L.push('');
      g.steps.forEach(function (s) {
        var rec = peek(s.id);
        var line = '- ' + s.id + ': ' + RESULT_LABEL[rec.result];
        if (rec.issues.length) line += ' (' + rec.issues.length + ' issue' + (rec.issues.length === 1 ? '' : 's') + ')';
        if (rec.notes && rec.notes.trim()) line += ' - ' + oneLine(rec.notes.trim());
        L.push(line);
      });
    });
  });
  L.push('');
  return L.join('\n');
}

function buildJson() {
  var out = { version: 1, hash: DATA.hash, exported: dateStamp(), run: state.run, steps: {} };
  Object.keys(state.steps).forEach(function (id) {
    var r = state.steps[id];
    if (r.result !== 'notrun' || (r.notes && r.notes.trim()) || r.issues.length) out.steps[id] = r;
  });
  return JSON.stringify(out, null, 2);
}

function download(name, text, type) {
  var blob = new Blob([text], { type: type });
  var url = URL.createObjectURL(blob);
  var a = document.createElement('a');
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  setTimeout(function () { document.body.removeChild(a); URL.revokeObjectURL(url); }, 500);
}

function doExport() {
  var d = dateStamp();
  download('acceptance-results-' + d + '.md', buildMarkdown(), 'text/markdown');
  setTimeout(function () { download('acceptance-results-' + d + '.json', buildJson(), 'application/json'); }, 300);
}

function showBanner(lines) {
  var b = document.getElementById('banner');
  b.textContent = '';
  if (!lines.length) return;
  var d = el('div', { class: 'banner' });
  lines.forEach(function (l) { d.appendChild(el('div', { text: l })); });
  var x = el('button', { type: 'button', text: 'Dismiss' });
  x.addEventListener('click', function () { b.textContent = ''; });
  d.appendChild(x);
  b.appendChild(d);
}

function doImport(text) {
  var o;
  try { o = JSON.parse(text); } catch (e) { showBanner(['Import failed: that file is not valid JSON.']); return; }
  if (!o || typeof o !== 'object' || typeof o.steps !== 'object' || o.steps === null) { showBanner(['Import failed: not an acceptance results file.']); return; }
  var msgs = [];
  var unknown = [];
  var merged = 0;
  Object.keys(o.steps).forEach(function (id) {
    if (!stepById[id]) { unknown.push(id); return; }
    var r = o.steps[id] || {};
    var rec = st(id);
    rec.result = RESULTS.some(function (x) { return x[0] === r.result; }) ? r.result : 'notrun';
    rec.notes = typeof r.notes === 'string' ? r.notes : '';
    rec.issues = Array.isArray(r.issues) ? r.issues.map(function (i) {
      var n = newIssue(stepById[id]);
      Object.keys(n).forEach(function (k) { if (typeof (i || {})[k] === 'string') n[k] = i[k]; });
      return n;
    }) : [];
    merged++;
  });
  if (o.run && typeof o.run === 'object') {
    RUNFIELDS.forEach(function (f) { if (typeof o.run[f[0]] === 'string' && o.run[f[0]]) state.run[f[0]] = o.run[f[0]]; });
  }
  msgs.push('Imported ' + merged + ' step(s).');
  if (o.hash && o.hash !== DATA.hash) msgs.push('Warning: this file came from a different version of the checklist (hash ' + o.hash + ', this form ' + DATA.hash + ').');
  if (unknown.length) msgs.push('Warning: ' + unknown.length + ' step ID(s) are not in this checklist and were skipped: ' + unknown.slice(0, 30).join(', ') + (unknown.length > 30 ? ', ...' : ''));
  saveNow();
  rebuild();
  showBanner(msgs);
}

function doClear() {
  if (!window.confirm('Clear ALL answers, issues and run info from this form? This cannot be undone (export first if unsure).')) return;
  state.run = {};
  state.steps = {};
  try { localStorage.removeItem(KEY); } catch (e) { /* ignore */ }
  rebuild();
}

function rebuild() {
  document.getElementById('runinfo').textContent = '';
  document.getElementById('content').textContent = '';
  els = {};
  groupEls = [];
  DATA.sections.forEach(function (s) { s.groups.forEach(function (g) { g.el = null; }); });
  buildRunInfo();
  buildContent();
  buildNav();
  steps.forEach(function (s) { refreshStep(s.id); });
  refreshNav();
  refreshHeader();
  updateFilterVisibility();
}

load();
buildTools();
rebuild();
document.getElementById('exportBtn').addEventListener('click', doExport);
document.getElementById('clearBtn').addEventListener('click', doClear);
document.getElementById('importBtn').addEventListener('click', function () { document.getElementById('importFile').click(); });
document.getElementById('importFile').addEventListener('change', function (ev) {
  var f = ev.target.files && ev.target.files[0];
  if (!f) return;
  var r = new FileReader();
  r.onload = function () { doImport(String(r.result)); ev.target.value = ''; };
  r.readAsText(f);
});
document.getElementById('menuBtn').addEventListener('click', function () { document.body.classList.toggle('menu'); });
window.addEventListener('resize', refreshHeader);
window.addEventListener('beforeunload', saveNow);
window.acceptanceForm = { markdown: buildMarkdown, json: buildJson, saveNow: saveNow, key: KEY };
})();
</script>
</body>
</html>
"""

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
