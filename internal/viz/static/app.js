(function () {
  var canvas = document.getElementById('graph');
  var ctx = canvas.getContext('2d');
  var state = {
    nodes: [],
    edges: [],
    nodeMap: new Map(),
    pos: new Map(),
    vel: new Map(),
    selected: null,
    hoveredID: null,
    neighborhoodDepth: 1,
    relationshipClickTimer: null,
    inspectorTab: 'inspector',
    optimizationRules: [],
    scale: 1,
    ox: 0,
    oy: 0,
    dragging: false,
    dragStart: null,
    running: false
  };
  var colors = {
    package: '#38bdf8',
    file: '#60a5fa',
    function: '#34d399',
    method: '#a7f3d0',
    type: '#c084fc',
    named_type: '#d8b4fe',
    interface: '#f9a8d4',
    variable: '#facc15',
    dependency: '#94a3b8',
    missing: '#fb7185'
  };
  var modeHelp = {
    packages: 'Packages view aggregates package import relationships only.',
    data_flow: 'Data Flow shows functions, methods, structs/classes, named types, and interfaces with call/use/type relationships.',
    custom: 'Custom exposes all filters for hand-built graph slices.'
  };
  var defaultEdgeTypes = [
    'calls', 'constructor', 'has_class', 'has_extvar', 'has_file', 'has_function',
    'has_interface', 'has_named_type', 'has_struct', 'implemented_by', 'implements',
    'imports_dependency', 'imports_package', 'inherited_by', 'inherits', 'methods',
    'uses_class', 'uses_dependency', 'uses_extvar', 'uses_interface', 'uses_named_type',
    'uses_package', 'uses_struct'
  ];
  var multiPickers = new Map();

  function el(id) { return document.getElementById(id); }
  function esc(s) {
    return String(s || '').replace(/[&<>"]/g, function (c) {
      return {'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;'}[c];
    });
  }
  function icon(name) {
    var paths = {
      'check': '<path d="M20 6 9 17l-5-5"/>',
      'chevron-down': '<path d="m6 9 6 6 6-6"/>',
      'git-branch': '<line x1="6" x2="6" y1="3" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>',
      'layers': '<path d="m12.83 2.18 8 4a1 1 0 0 1 0 1.79l-8 4a2 2 0 0 1-1.79 0l-8-4a1 1 0 0 1 0-1.79l8-4a2 2 0 0 1 1.79 0Z"/><path d="m22 12-9.17 4.59a2 2 0 0 1-1.79 0L2 12"/><path d="m22 17-9.17 4.59a2 2 0 0 1-1.79 0L2 17"/>',
      'plus': '<path d="M5 12h14"/><path d="M12 5v14"/>',
      'search': '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
      'settings-2': '<path d="M20 7h-9"/><path d="M14 17H5"/><circle cx="17" cy="17" r="3"/><circle cx="7" cy="7" r="3"/>',
      'star': '<polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/>',
      'toggle-left': '<rect width="20" height="12" x="2" y="6" rx="6" ry="6"/><circle cx="8" cy="12" r="2"/>',
      'toggle-right': '<rect width="20" height="12" x="2" y="6" rx="6" ry="6"/><circle cx="16" cy="12" r="2"/>',
      'trash': '<path d="M3 6h18"/><path d="M8 6V4h8v2"/><path d="m19 6-1 14H6L5 6"/>',
      'waves-arrow-down': '<path d="M3 6c2 0 2-2 4-2s2 2 4 2 2-2 4-2 2 2 4 2 2-2 4-2"/><path d="M3 12c2 0 2-2 4-2s2 2 4 2 2-2 4-2 2 2 4 2 2-2 4-2"/><path d="M12 14v7"/><path d="m8 17 4 4 4-4"/>',
      'x': '<path d="M18 6 6 18"/><path d="m6 6 12 12"/>'
    };
    return '<svg class="icon icon-' + esc(name) + '" viewBox="0 0 24 24" aria-hidden="true">' + (paths[name] || '') + '</svg>';
  }
  function params(obj) {
    var p = new URLSearchParams();
    Object.keys(obj).forEach(function (k) {
      if (Array.isArray(obj[k])) {
        obj[k].forEach(function (v) { if (v !== '') p.append(k, v); });
      } else if (obj[k] !== '') {
        p.set(k, obj[k]);
      }
    });
    return p.toString();
  }
  function api(path) {
    return fetch(path).then(function (r) {
      return r.json().then(function (body) {
        if (!r.ok) throw new Error(body.error || r.statusText);
        return body;
      });
    });
  }
  function apiJSON(path, method, body) {
    return fetch(path, {
      method: method,
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(body)
    }).then(function (r) {
      return r.json().then(function (responseBody) {
        if (!r.ok) throw new Error(responseBody.error || r.statusText);
        return responseBody;
      });
    });
  }
  function selectedValues(select) {
    return Array.prototype.slice.call(select.selectedOptions).map(function (option) {
      return option.value;
    });
  }

  var operationKinds = [
    {value: 'less_equal_incoming', label: 'Less than or equal to X Incoming Connections', type: 'number'},
    {value: 'less_equal_outgoing', label: 'Less than or equal to X Outgoing Connections', type: 'number'},
    {value: 'less_equal_connections', label: 'Less than or equal to X Connections', type: 'number'},
    {value: 'non_exported', label: 'Non-Exported Resource', type: 'none'},
    {value: 'resource_kind', label: 'ResourceKind is X', type: 'kind'},
    {value: 'less_equal_lines', label: 'Less than or equal to X Lines', type: 'number'}
  ];
  var resourceKinds = ['package', 'file', 'function', 'method', 'type', 'named_type', 'interface', 'variable', 'dependency'];

  function operationMeta(kind) {
    return operationKinds.find(function (op) { return op.value === kind; }) || operationKinds[0];
  }

  function activeOptimizationRules() {
    return state.optimizationRules.filter(function (rule) { return rule.active && rule.operations && rule.operations.length; });
  }

  function optimizationRulesParam() {
    var rules = activeOptimizationRules();
    return rules.length ? JSON.stringify(rules) : '';
  }

  function newOptimizationRule() {
    return {
      id: 'rule-' + Date.now().toString(36),
      active: true,
      favorite: false,
      operations: [{kind: 'non_exported', value: ''}]
    };
  }

  function loadOptimizationRules() {
    return api('/api/optimization-rules').then(function (rules) {
      state.optimizationRules = Array.isArray(rules) ? rules : [];
      renderOptimizationRules();
    }).catch(function (err) {
      showError(err);
      renderOptimizationRules();
    });
  }

  function saveFavoriteOptimizationRules() {
    var favorites = state.optimizationRules.filter(function (rule) { return rule.favorite; });
    return apiJSON('/api/optimization-rules', 'PUT', favorites).catch(showError);
  }

  function renderOptimizationRules() {
    var toggle = el('graphOptimizationToggle');
    if (toggle) toggle.innerHTML = icon('settings-2') + '<span>Graph Optimization</span>';
    var list = el('optimizationRules');
    if (!list) return;
    if (!state.optimizationRules.length) {
      list.innerHTML = '<div class="empty small">No optimization rules.</div>';
      return;
    }
    list.innerHTML = state.optimizationRules.map(function (rule, ruleIndex) {
      var ops = (rule.operations || []).map(function (op, opIndex) {
        var meta = operationMeta(op.kind);
        var valueControl = '';
        if (meta.type === 'number') {
          valueControl = '<input class="optimizationValue" data-rule-index="' + ruleIndex + '" data-op-index="' + opIndex + '" type="number" min="0" value="' + esc(op.value || '0') + '">';
        } else if (meta.type === 'kind') {
          valueControl = '<select class="optimizationValue" data-rule-index="' + ruleIndex + '" data-op-index="' + opIndex + '">' + resourceKinds.map(function (kind) {
            return '<option value="' + esc(kind) + '"' + (op.value === kind ? ' selected' : '') + '>' + esc(kind) + '</option>';
          }).join('') + '</select>';
        }
        return '<div class="optimizationOperation">' +
          '<select class="optimizationKind" data-rule-index="' + ruleIndex + '" data-op-index="' + opIndex + '">' + operationKinds.map(function (option) {
            return '<option value="' + esc(option.value) + '"' + (op.kind === option.value ? ' selected' : '') + '>' + esc(option.label) + '</option>';
          }).join('') + '</select>' +
          valueControl +
          '<button type="button" class="iconButton" data-action="remove-op" data-rule-index="' + ruleIndex + '" data-op-index="' + opIndex + '">' + icon('x') + '</button>' +
          '</div>';
      }).join('');
      return '<section class="optimizationRule' + (rule.active ? '' : ' inactive') + '">' +
        '<div class="optimizationRuleHeader">' +
        '<button type="button" class="iconButton" data-action="toggle-rule" data-rule-index="' + ruleIndex + '">' + icon(rule.active ? 'toggle-right' : 'toggle-left') + '</button>' +
        '<strong>Rule ' + (ruleIndex + 1) + '</strong>' +
        '<button type="button" class="iconButton starButton' + (rule.favorite ? ' favorite' : '') + '" data-action="favorite-rule" data-rule-index="' + ruleIndex + '">' + icon('star') + '</button>' +
        '<button type="button" class="iconButton dangerButton" data-action="delete-rule" data-rule-index="' + ruleIndex + '">' + icon('trash') + '</button>' +
        '</div>' +
        '<div class="optimizationOperations">' + (ops || '<p class="muted">No operations. Add one to activate matching.</p>') + '</div>' +
        '<button type="button" class="addOperation" data-action="add-op" data-rule-index="' + ruleIndex + '">' + icon('plus') + 'Add Operation</button>' +
        '</section>';
    }).join('');
  }

  function updateOptimizationRule(updateFavorites) {
    renderOptimizationRules();
    if (updateFavorites) saveFavoriteOptimizationRules();
    loadGraph();
  }

  function handleOptimizationClick(e) {
    var actionEl = e.target.closest('[data-action]');
    if (!actionEl || !el('optimizationMenu').contains(actionEl)) return;
    var ruleIndex = Number(actionEl.dataset.ruleIndex);
    var opIndex = Number(actionEl.dataset.opIndex);
    var rule = state.optimizationRules[ruleIndex];
    if (actionEl.dataset.action === 'add-op' && rule) {
      rule.operations = rule.operations || [];
      rule.operations.push({kind: 'less_equal_connections', value: '1'});
      updateOptimizationRule(false);
    } else if (actionEl.dataset.action === 'remove-op' && rule) {
      rule.operations.splice(opIndex, 1);
      updateOptimizationRule(false);
    } else if (actionEl.dataset.action === 'toggle-rule' && rule) {
      rule.active = !rule.active;
      updateOptimizationRule(rule.favorite);
    } else if (actionEl.dataset.action === 'favorite-rule' && rule) {
      rule.favorite = !rule.favorite;
      updateOptimizationRule(true);
    } else if (actionEl.dataset.action === 'delete-rule') {
      var wasFavorite = rule && rule.favorite;
      state.optimizationRules.splice(ruleIndex, 1);
      updateOptimizationRule(wasFavorite);
    }
  }

  function handleOptimizationChange(e) {
    var ruleIndex = Number(e.target.dataset.ruleIndex);
    var opIndex = Number(e.target.dataset.opIndex);
    var rule = state.optimizationRules[ruleIndex];
    if (!rule || !rule.operations || !rule.operations[opIndex]) return;
    if (e.target.classList.contains('optimizationKind')) {
      var meta = operationMeta(e.target.value);
      rule.operations[opIndex].kind = e.target.value;
      rule.operations[opIndex].value = meta.type === 'number' ? '1' : meta.type === 'kind' ? resourceKinds[0] : '';
    } else if (e.target.classList.contains('optimizationValue')) {
      rule.operations[opIndex].value = e.target.value;
    }
    updateOptimizationRule(rule.favorite);
  }

  function enhanceMultiSelect(select, opts) {
    var picker = document.createElement('div');
    picker.className = 'multiPicker';
    picker.innerHTML =
      '<button type="button" class="multiPickerButton">' + icon(opts.icon) + '<span class="pickerText"><strong>' + esc(opts.title) + '</strong><small></small></span><span class="pickerChevron">' + icon('chevron-down') + '</span></button>' +
      '<div class="multiPickerMenu">' +
      (opts.search ? '<div class="multiPickerSearchWrap">' + icon('search') + '<input class="multiPickerSearch" placeholder="Search edge types"></div>' : '') +
      '<div class="multiPickerActions"><button type="button" data-action="all">' + icon('check') + 'All</button><button type="button" data-action="none">' + icon('x') + 'None</button></div>' +
      '<div class="multiPickerOptions"></div>' +
      '</div>';
    select.insertAdjacentElement('afterend', picker);
    multiPickers.set(select.id, {select: select, picker: picker, opts: opts});

    picker.querySelector('.multiPickerButton').addEventListener('click', function () {
      var willOpen = !picker.classList.contains('open');
      closeMultiPickers();
      picker.classList.toggle('open', willOpen);
      if (willOpen && opts.search) picker.querySelector('.multiPickerSearch').focus();
    });
    picker.querySelector('.multiPickerActions').addEventListener('click', function (e) {
      var action = e.target.dataset.action;
      if (!action) return;
      Array.prototype.forEach.call(select.options, function (option) {
        option.selected = action === 'all';
      });
      refreshMultiSelect(select.id);
    });
    picker.querySelector('.multiPickerOptions').addEventListener('change', function (e) {
      if (e.target.type !== 'checkbox') return;
      var option = Array.prototype.find.call(select.options, function (opt) {
        return opt.value === e.target.value;
      });
      if (option) option.selected = e.target.checked;
      refreshMultiSelect(select.id);
    });
    if (opts.search) {
      picker.querySelector('.multiPickerSearch').addEventListener('input', function (e) {
        filterMultiSelect(picker, e.target.value);
      });
    }
    refreshMultiSelect(select.id);
  }

  function refreshMultiSelect(id) {
    var entry = multiPickers.get(id);
    if (!entry) return;
    var select = entry.select;
    var opts = entry.opts;
    var options = Array.prototype.slice.call(select.options);
    entry.picker.querySelector('.multiPickerOptions').innerHTML = options.map(function (option) {
      var color = opts.color(option.value);
      return '<label class="multiPickerOption' + (option.selected ? ' selected' : '') + '" data-label="' + esc(option.textContent.toLowerCase()) + '">' +
        '<input type="checkbox" value="' + esc(option.value) + '"' + (option.selected ? ' checked' : '') + '>' +
        '<span class="optionDot" style="--dot:' + esc(color) + '"></span>' +
        '<span>' + esc(option.textContent) + '</span>' +
        '</label>';
    }).join('') || '<div class="emptyPicker">No options available.</div>';
    updateMultiSelectSummary(entry);
    var search = entry.picker.querySelector('.multiPickerSearch');
    if (search) filterMultiSelect(entry.picker, search.value);
  }

  function updateMultiSelectSummary(entry) {
    var selected = selectedValues(entry.select);
    var summary = selected.length ? selected.slice(0, 2).join(', ') + (selected.length > 2 ? ' +' + (selected.length - 2) : '') : entry.opts.emptyLabel;
    entry.picker.querySelector('.multiPickerButton small').textContent = summary;
  }

  function filterMultiSelect(picker, q) {
    var needle = q.trim().toLowerCase();
    Array.prototype.forEach.call(picker.querySelectorAll('.multiPickerOption'), function (row) {
      row.classList.toggle('hidden', !!needle && row.dataset.label.indexOf(needle) === -1);
    });
  }

  function closeMultiPickers() {
    multiPickers.forEach(function (entry) {
      entry.picker.classList.remove('open');
    });
  }

  function resize() {
    var rect = canvas.getBoundingClientRect();
    var dpr = window.devicePixelRatio || 1;
    canvas.width = Math.max(1, Math.floor(rect.width * dpr));
    canvas.height = Math.max(1, Math.floor(rect.height * dpr));
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    draw();
  }

  function loadSummary() {
    api('/api/summary').then(function (s) {
      el('subtitle').textContent = (s.language || 'unknown') + ' topology';
      populateEdgeTypes(s.edge_types || {});
      el('status').textContent = 'Ready. Load a standard view or customize a slice.';
    }).catch(showError);
  }

  function populateEdgeTypes(edgeCounts) {
    var seen = new Set(defaultEdgeTypes);
    Object.keys(edgeCounts).forEach(function (edgeType) { seen.add(edgeType); });
    el('edgeKind').innerHTML = Array.from(seen).sort().map(function (edgeType) {
      var count = edgeCounts[edgeType];
      var label = count == null ? edgeType : edgeType + ' (' + count + ')';
      return '<option value="' + esc(edgeType) + '">' + esc(label) + '</option>';
    }).join('');
    refreshMultiSelect('edgeKind');
  }

  function withOptimizationRules(filter) {
    var rules = optimizationRulesParam();
    if (rules) filter.optimization_rules = rules;
    return filter;
  }

  function currentFilter() {
    var mode = el('mode').value;
    var filter = {
      mode: mode,
      q: el('query').value.trim()
    };
    if (mode === 'packages') {
      filter.limit = '1000';
      return withOptimizationRules(filter);
    }
    if (mode === 'data_flow') {
      filter.limit = '700';
      return withOptimizationRules(filter);
    }
    filter.kind = selectedValues(el('kind'));
    filter.edge_kind = selectedValues(el('edgeKind'));
    filter.path = el('path').value.trim();
    filter.limit = el('limit').value.trim() || '300';
    if (document.getElementById('strictEdges') && document.getElementById('strictEdges').checked) {
      filter.strict_edges = true;
    }
    delete filter.mode;
    return withOptimizationRules(filter);
  }

  function loadGraph() {
    var q = params(currentFilter());
    el('status').textContent = 'Loading graph view...';
    api('/api/graph?' + q).then(setGraph).catch(showError);
  }

  function loadNeighborhood(id, depth) {
    var targetDepth = depth || state.neighborhoodDepth;
    state.neighborhoodDepth = targetDepth;
    updateDepthControls();
    el('status').textContent = 'Loading neighborhood...';
    var mode = el('mode').value;
    var edgeKinds = mode === 'custom' ? selectedValues(el('edgeKind')) : [];
    var q = params({
      id: id,
      depth: String(targetDepth),
      direction: 'both',
      mode: mode,
      kind: mode === 'custom' ? selectedValues(el('kind')) : [],
      edge_kind: edgeKinds,
      limit: '700',
      optimization_rules: optimizationRulesParam()
    });
    api('/api/neighborhood?' + q).then(function (data) {
      setGraph(data);
      selectNode(id);
    }).catch(showError);
  }

  function updateDepthControls() {
    var controls = el('depthControls');
    if (!controls) return;
    var hasSelection = !!state.selected;
    controls.classList.toggle('disabled', !hasSelection);
    Array.prototype.forEach.call(controls.querySelectorAll('.depthButton'), function (button) {
      var isActive = Number(button.dataset.depth) === state.neighborhoodDepth;
      button.classList.toggle('active', isActive);
      button.disabled = !hasSelection;
    });
  }

  function setGraph(data) {
    state.nodes = data.nodes || [];
    state.edges = data.edges || [];
    if (document.getElementById('strictEdges') && document.getElementById('strictEdges').checked) {
      var connectedIDs = new Set();
      state.edges.forEach(function (e) {
        connectedIDs.add(e.source);
        connectedIDs.add(e.target);
      });
      state.nodes = state.nodes.filter(function (n) {
        return connectedIDs.has(n.id);
      });
    }
    state.nodeMap = new Map(state.nodes.map(function (n) { return [n.id, n]; }));
    state.pos = new Map();
    state.vel = new Map();
    state.selected = null;
    state.hoveredID = null;
    updateDepthControls();
    seedPositions();
    el('inspector').innerHTML = '<div class="empty">Select a node.</div>';
    el('codePanel').innerHTML = '<div class="empty">No source available.</div>';
    el('codeTab').classList.add('disabled');
    switchInspectorTab('inspector');
    el('status').textContent = state.nodes.length + ' nodes, ' + state.edges.length + ' edges' + (data.truncated ? ' (truncated)' : '');
    runSimulation(180);
  }

  function seedPositions() {
    var rect = canvas.getBoundingClientRect();
    var radius = Math.max(80, Math.min(rect.width, rect.height) * 0.34);
    state.nodes.forEach(function (n, i) {
      var a = (Math.PI * 2 * i) / Math.max(1, state.nodes.length);
      state.pos.set(n.id, {x: Math.cos(a) * radius, y: Math.sin(a) * radius});
      state.vel.set(n.id, {x: 0, y: 0});
    });
  }

  function runSimulation(ticks) {
    if (state.running) return;
    state.running = true;
    var remaining = ticks;
    function frame() {
      if (remaining-- > 0) {
        tick();
        draw();
        requestAnimationFrame(frame);
      } else {
        state.running = false;
        draw();
      }
    }
    requestAnimationFrame(frame);
  }

  function tick() {
    var nodes = state.nodes;
    var repulseLimit = Math.min(nodes.length, 350);
    for (var i = 0; i < repulseLimit; i++) {
      for (var j = i + 1; j < repulseLimit; j++) {
        var a = state.pos.get(nodes[i].id);
        var b = state.pos.get(nodes[j].id);
        var dx = a.x - b.x;
        var dy = a.y - b.y;
        var d2 = dx * dx + dy * dy + 20;
        var f = Math.min(2.2, 900 / d2);
        var d = Math.sqrt(d2);
        push(nodes[i].id, dx / d * f, dy / d * f);
        push(nodes[j].id, -dx / d * f, -dy / d * f);
      }
    }
    state.edges.forEach(function (e) {
      var a = state.pos.get(e.source);
      var b = state.pos.get(e.target);
      if (!a || !b) return;
      var dx = b.x - a.x;
      var dy = b.y - a.y;
      var d = Math.sqrt(dx * dx + dy * dy) || 1;
      var want = 100;
      var f = (d - want) * 0.008;
      push(e.source, dx / d * f, dy / d * f);
      push(e.target, -dx / d * f, -dy / d * f);
    });
    nodes.forEach(function (n) {
      var p = state.pos.get(n.id);
      var v = state.vel.get(n.id);
      v.x += -p.x * 0.001;
      v.y += -p.y * 0.001;
      v.x *= 0.82;
      v.y *= 0.82;
      p.x += v.x;
      p.y += v.y;
    });
  }

  function push(id, x, y) {
    var v = state.vel.get(id);
    if (v) {
      v.x += x;
      v.y += y;
    }
  }

  function draw() {
    var rect = canvas.getBoundingClientRect();
    ctx.clearRect(0, 0, rect.width, rect.height);
    ctx.save();
    ctx.translate(rect.width / 2 + state.ox, rect.height / 2 + state.oy);
    ctx.scale(state.scale, state.scale);
    ctx.lineWidth = 1 / state.scale;
    ctx.globalAlpha = 0.45;
    state.edges.forEach(function (e) {
      var a = state.pos.get(e.source);
      var b = state.pos.get(e.target);
      if (!a || !b) return;
      ctx.strokeStyle = edgeColor(e.type);
      ctx.beginPath();
      ctx.moveTo(a.x, a.y);
      ctx.lineTo(b.x, b.y);
      ctx.stroke();
    });
    ctx.globalAlpha = 1;
    state.nodes.forEach(function (n) {
      var p = state.pos.get(n.id);
      if (!p) return;
      var r = radius(n);
      ctx.fillStyle = colors[n.kind] || '#e2e8f0';
      ctx.beginPath();
      ctx.arc(p.x, p.y, r, 0, Math.PI * 2);
      ctx.fill();
      if (n.warning_count > 0 || n.bug_count > 0) {
        ctx.strokeStyle = n.bug_count > 0 ? '#fb7185' : '#facc15';
        ctx.lineWidth = 3 / state.scale;
        ctx.stroke();
      }
      if (state.hoveredID === n.id) {
        ctx.strokeStyle = '#74d4ff';
        ctx.lineWidth = 7 / state.scale;
        ctx.stroke();
      }
      if (state.selected && state.selected.id === n.id) {
        ctx.strokeStyle = '#ffffff';
        ctx.lineWidth = 4 / state.scale;
        ctx.stroke();
      }
      if (state.scale > 0.78 || r > 7 || (state.selected && state.selected.id === n.id) || state.hoveredID === n.id) {
        ctx.fillStyle = '#e8edf6';
        ctx.font = (12 / state.scale) + 'px sans-serif';
        ctx.fillText(n.name || n.id, p.x + r + 4 / state.scale, p.y + 4 / state.scale);
      }
    });
    ctx.restore();
  }

  function radius(n) {
    return Math.max(4, Math.min(14, 4 + Math.sqrt((n.in_degree || 0) + (n.out_degree || 0))));
  }
  function edgeColor(t) {
    var h = 0;
    for (var i = 0; i < t.length; i++) h = (h * 31 + t.charCodeAt(i)) % 360;
    return 'hsl(' + h + ', 70%, 62%)';
  }

  function selectNode(id) {
    var n = state.nodeMap.get(id) || {id: id};
    state.selected = n;
    updateDepthControls();
    draw();
    var q = params({optimization_rules: optimizationRulesParam()});
    api('/api/node/' + encodeURIComponent(id) + (q ? '?' + q : '')).then(renderInspector).catch(showError);
  }

  function setHoveredID(id) {
    if (state.hoveredID === id) return;
    state.hoveredID = id;
    syncRelationshipHover();
    draw();
  }

  function syncRelationshipHover() {
    Array.prototype.forEach.call(document.querySelectorAll('.relationshipNode'), function (item) {
      item.classList.toggle('hovered', !!state.hoveredID && item.dataset.nodeId === state.hoveredID);
    });
  }

  function activeKindSet() {
    var mode = el('mode').value;
    var kinds;
    if (mode === 'packages') {
      kinds = ['package'];
    } else if (mode === 'data_flow') {
      kinds = ['function', 'method', 'type', 'interface'];
    } else {
      kinds = selectedValues(el('kind'));
    }
    if (!kinds.length) return null;
    return new Set(kinds);
  }

  function formatLines(start, end) {
    if (!start && !end) return '';
    if (!end || end === start) return String(start || end);
    return start + '-' + end;
  }

  function formatRelationType(type) {
    return String(type || '').split('_').map(function (part) {
      return part.charAt(0).toUpperCase() + part.slice(1);
    }).join(' ');
  }

  function renderRelationshipGroups(title, edges, direction) {
    var kinds = activeKindSet();
    var groups = new Map();
    (edges || []).forEach(function (edge) {
      var otherID = direction === 'outgoing' ? edge.target : edge.source;
      var otherKind = direction === 'outgoing' ? edge.target_kind : edge.source_kind;
      if (kinds && !kinds.has(otherKind)) return;
      if (!groups.has(edge.type)) groups.set(edge.type, []);
      groups.get(edge.type).push(otherID);
    });
    if (!groups.size) return '<h2>' + esc(title) + '</h2><p class="muted">None</p>';

    var html = '<h2>' + esc(title) + '</h2><div class="relationships">';
    Array.from(groups.keys()).sort().forEach(function (type) {
      var ids = groups.get(type).sort().slice(0, 80);
      html += '<h3>' + esc(formatRelationType(type)) + '</h3><ul>' + ids.map(function (id) {
        return '<li class="relationshipNode" data-node-id="' + esc(id) + '">' + esc(id) + '</li>';
      }).join('') + '</ul>';
    });
    return html + '</div>';
  }

  function renderIncludes(includes) {
    if (!includes || !includes.length) return '';
    return '<h2>Includes</h2><div class="relationships"><ul>' + includes.map(function (node) {
      return '<li class="relationshipNode includedNode" data-node-id="' + esc(node.id) + '">' +
        '<strong>' + esc(node.name || node.id) + '</strong>' +
        '<span>' + esc(node.kind) + '</span>' +
        '<p>' + esc(node.description || 'No description.') + '</p>' +
        '</li>';
    }).join('') + '</ul></div>';
  }

  function renderInspector(data) {
    var n = data.node;
    var lines = formatLines(n.starts_at, n.ends_at);
    el('inspector').innerHTML =
      '<div class="nodeTitle"><span class="kind">' + esc(n.kind) + '</span><h3>' + esc(n.name || n.id) + '</h3></div>' +
      '<div class="kv">' +
      '<div><span>ID</span>' + esc(n.id) + '</div>' +
      '<div><span>Path</span>' + esc(n.path || 'none') + '</div>' +
      (lines ? '<div><span>Lines</span>' + esc(lines) + '</div>' : '') +
      '<div><span>Degree</span>in ' + esc(n.in_degree) + ', out ' + esc(n.out_degree) + '</div>' +
      '</div>' +
      '<h2>Description</h2><pre>' + esc(n.description || 'No description.') + '</pre>' +
      renderIncludes(n.includes) +
      renderRelationshipGroups('Outgoing', data.outgoing, 'outgoing') +
      renderRelationshipGroups('Incoming', data.incoming, 'incoming');
    syncRelationshipHover();

    if (data.code) {
      el('codePanel').innerHTML = '<pre class="codeBlock">' + esc(data.code) + '</pre>';
      el('codeTab').classList.remove('disabled');
    } else {
      el('codePanel').innerHTML = '<div class="empty">No source available for this resource kind.</div>';
      el('codeTab').classList.add('disabled');
    }
    switchInspectorTab(data.code ? state.inspectorTab : 'inspector');
  }

  function switchInspectorTab(tab) {
    if (tab === 'inspector') {
      el('inspectorTab').classList.add('active');
      el('codeTab').classList.remove('active');
      el('inspector').classList.remove('hidden');
      el('codePanel').classList.add('hidden');
    } else {
      if (el('codeTab').classList.contains('disabled')) return;
      el('inspectorTab').classList.remove('active');
      el('codeTab').classList.add('active');
      el('inspector').classList.add('hidden');
      el('codePanel').classList.remove('hidden');
    }
    state.inspectorTab = tab;
  }

  function screenToWorld(x, y) {
    var rect = canvas.getBoundingClientRect();
    return {
      x: (x - rect.left - rect.width / 2 - state.ox) / state.scale,
      y: (y - rect.top - rect.height / 2 - state.oy) / state.scale
    };
  }
  function nearest(x, y) {
    var w = screenToWorld(x, y);
    var best = null;
    var bestD = Infinity;
    state.nodes.forEach(function (n) {
      var p = state.pos.get(n.id);
      if (!p) return;
      var dx = p.x - w.x;
      var dy = p.y - w.y;
      var d = dx * dx + dy * dy;
      if (d < bestD) {
        bestD = d;
        best = n;
      }
    });
    return bestD < Math.pow(18 / state.scale, 2) ? best : null;
  }
  function fit() {
    state.scale = 1;
    state.ox = 0;
    state.oy = 0;
    draw();
  }
  function showError(err) {
    el('status').textContent = 'Error: ' + err.message;
  }
  function updateModeControls() {
    var mode = el('mode').value;
    el('customControls').classList.toggle('hidden', mode !== 'custom');
    el('modeHelp').textContent = modeHelp[mode] || '';
    el('loadGraph').textContent = mode === 'custom' ? 'Load Custom Slice' : 'Load ' + el('mode').selectedOptions[0].text;
  }

  canvas.addEventListener('mousedown', function (e) {
    state.dragging = true;
    state.dragStart = {x: e.clientX, y: e.clientY, ox: state.ox, oy: state.oy};
  });
  window.addEventListener('mouseup', function () { state.dragging = false; });
  canvas.addEventListener('mousemove', function (e) {
    if (state.dragging) return;
    var n = nearest(e.clientX, e.clientY);
    canvas.style.cursor = n ? 'pointer' : '';
    setHoveredID(n ? n.id : null);
  });
  canvas.addEventListener('mouseleave', function () {
    canvas.style.cursor = '';
    setHoveredID(null);
  });
  window.addEventListener('mousemove', function (e) {
    if (!state.dragging) return;
    state.ox = state.dragStart.ox + e.clientX - state.dragStart.x;
    state.oy = state.dragStart.oy + e.clientY - state.dragStart.y;
    draw();
  });
  canvas.addEventListener('click', function (e) {
    var n = nearest(e.clientX, e.clientY);
    if (n) selectNode(n.id);
  });
  canvas.addEventListener('dblclick', function (e) {
    var n = nearest(e.clientX, e.clientY);
    if (n) {
      loadNeighborhood(n.id);
    } else {
      loadGraph();
    }
  });
  canvas.addEventListener('wheel', function (e) {
    e.preventDefault();
    state.scale *= e.deltaY > 0 ? 0.9 : 1.1;
    state.scale = Math.max(0.15, Math.min(4, state.scale));
    draw();
  }, {passive: false});
  el('inspector').addEventListener('click', function (e) {
    var item = e.target.closest('.relationshipNode');
    if (!item || !el('inspector').contains(item)) return;
    var id = item.dataset.nodeId;
    clearTimeout(state.relationshipClickTimer);
    state.relationshipClickTimer = setTimeout(function () {
      state.relationshipClickTimer = null;
      selectNode(id);
    }, 240);
  });
  el('inspector').addEventListener('dblclick', function (e) {
    var item = e.target.closest('.relationshipNode');
    if (!item || !el('inspector').contains(item)) return;
    clearTimeout(state.relationshipClickTimer);
    state.relationshipClickTimer = null;
    loadNeighborhood(item.dataset.nodeId);
  });
  el('inspector').addEventListener('mouseover', function (e) {
    var item = e.target.closest('.relationshipNode');
    if (item && el('inspector').contains(item)) setHoveredID(item.dataset.nodeId);
  });
  el('inspector').addEventListener('mouseout', function (e) {
    var item = e.target.closest('.relationshipNode');
    if (item && !item.contains(e.relatedTarget)) setHoveredID(null);
  });
  el('inspectorTab').addEventListener('click', function () { switchInspectorTab('inspector'); });
  el('codeTab').addEventListener('click', function () { switchInspectorTab('code'); });
  el('mode').addEventListener('change', function () {
    updateModeControls();
    loadGraph();
  });
  el('loadGraph').onclick = loadGraph;
  el('depthControls').addEventListener('click', function (e) {
    var button = e.target.closest('.depthButton');
    if (!button || button.disabled || !state.selected) return;
    loadNeighborhood(state.selected.id, Number(button.dataset.depth));
  });
  el('graphOptimizationToggle').onclick = function () {
    el('optimizationMenu').classList.toggle('hidden');
  };
  el('addOptimizationRule').onclick = function () {
    state.optimizationRules.push(newOptimizationRule());
    updateOptimizationRule(false);
  };
  el('optimizationRules').addEventListener('click', handleOptimizationClick);
  el('optimizationRules').addEventListener('change', handleOptimizationChange);
  el('clear').onclick = function () {
    state.nodes = [];
    state.edges = [];
    state.nodeMap = new Map();
    state.selected = null;
    state.hoveredID = null;
    updateDepthControls();
    el('inspector').innerHTML = '<div class="empty">Select a node.</div>';
    el('codePanel').innerHTML = '<div class="empty">No source available.</div>';
    el('codeTab').classList.add('disabled');
    switchInspectorTab('inspector');
    draw();
  };
  el('zoomIn').onclick = function () { state.scale *= 1.15; draw(); };
  el('zoomOut').onclick = function () { state.scale *= 0.85; draw(); };
  el('fit').onclick = fit;
  el('query').addEventListener('keydown', function (e) {
    if (e.key === 'Enter') loadGraph();
  });
  window.addEventListener('resize', resize);
  document.addEventListener('click', function (e) {
    if (!e.target.closest('.multiPicker')) closeMultiPickers();
  });
  resize();
  el('depthIcon').innerHTML = icon('waves-arrow-down');
  updateDepthControls();
  updateModeControls();
  enhanceMultiSelect(el('kind'), {
    title: 'Node kinds',
    emptyLabel: 'All kinds',
    search: false,
    icon: 'layers',
    color: function (value) { return colors[value] || '#e2e8f0'; }
  });
  enhanceMultiSelect(el('edgeKind'), {
    title: 'Edge types',
    emptyLabel: 'All edge types',
    search: true,
    icon: 'git-branch',
    color: edgeColor
  });
  loadSummary();
  loadOptimizationRules().then(loadGraph);
})();
