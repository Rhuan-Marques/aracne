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
    languages: [],
    chatSessions: [],
    chatSession: null,
    chatAgents: [],
    providerConfig: null,
    selectedChatProvider: '',
    selectedChatModel: '',
    providerEditorID: null,
    providerEditorType: 'supported',
    chatView: 'graph',
    chatThinking: false,
    chatStreaming: null,
    scale: 1,
    ox: 0,
    oy: 0,
    dragging: false,
    dragMoved: false,
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
  function routeNodeColor(n) {}
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

  function renderInlineMarkdown(s) {
    return esc(s)
      .replace(/`([^`]+)`/g, '<code>$1</code>')
      .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
      .replace(/__([^_]+)__/g, '<strong>$1</strong>')
      .replace(/\*([^*]+)\*/g, '<em>$1</em>')
      .replace(/_([^_]+)_/g, '<em>$1</em>');
  }

  function renderMarkdown(text) {
    var lines = String(text || '').replace(/\r\n/g, '\n').split('\n');
    var html = [];
    var inCode = false;
    var codeLines = [];
    var listType = '';

    function closeList() {
      if (listType) {
        html.push('</' + listType + '>');
        listType = '';
      }
    }

    lines.forEach(function (line) {
      var fence = line.match(/^\s*```/);
      if (fence) {
        closeList();
        if (inCode) {
          html.push('<pre><code>' + esc(codeLines.join('\n')) + '</code></pre>');
          codeLines = [];
          inCode = false;
        } else {
          inCode = true;
        }
        return;
      }
      if (inCode) {
        codeLines.push(line);
        return;
      }

      if (!line.trim()) {
        closeList();
        return;
      }

      var heading = line.match(/^(#{1,6})\s+(.+)$/);
      if (heading) {
        closeList();
        var level = heading[1].length;
        html.push('<h' + level + '>' + renderInlineMarkdown(heading[2]) + '</h' + level + '>');
        return;
      }

      var unordered = line.match(/^\s*[-*]\s+(.+)$/);
      if (unordered) {
        if (listType !== 'ul') {
          closeList();
          html.push('<ul>');
          listType = 'ul';
        }
        html.push('<li>' + renderInlineMarkdown(unordered[1]) + '</li>');
        return;
      }

      var ordered = line.match(/^\s*\d+\.\s+(.+)$/);
      if (ordered) {
        if (listType !== 'ol') {
          closeList();
          html.push('<ol>');
          listType = 'ol';
        }
        html.push('<li>' + renderInlineMarkdown(ordered[1]) + '</li>');
        return;
      }

      closeList();
      html.push('<p>' + renderInlineMarkdown(line) + '</p>');
    });

    if (inCode) html.push('<pre><code>' + esc(codeLines.join('\n')) + '</code></pre>');
    closeList();
    return html.join('');
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
      'bot': '<path d="M12 8V4"/><path d="M8 4h8"/><rect x="5" y="8" width="14" height="12" rx="3"/><path d="M9 13h.01"/><path d="M15 13h.01"/><path d="M10 17h4"/>',
      'bug': '<path d="m8 2 1.88 1.88"/><path d="M14.12 3.88 16 2"/><path d="M9 7.13v-1a3 3 0 0 1 6 0v1"/><path d="M12 20c-3.3 0-6-2.7-6-6v-3a4 4 0 0 1 4-4h4a4 4 0 0 1 4 4v3c0 3.3-2.7 6-6 6"/><path d="M6 13H2"/><path d="M22 13h-4"/><path d="M6.7 17 3 20"/><path d="M20.9 20 17.3 17"/>',
      'file-text': '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/><path d="M16 13H8"/><path d="M16 17H8"/><path d="M10 9H8"/>',
      'scale': '<path d="m16 16 3-8 3 8c-.87.65-1.92 1-3 1s-2.13-.35-3-1"/><path d="m2 16 3-8 3 8c-.87.65-1.92 1-3 1s-2.13-.35-3-1"/><path d="M7 21h10"/><path d="M12 3v18"/><path d="M3 7h2c2 0 5-1 7-2 2 1 5 2 7 2h2"/>',
      'sparkles': '<path d="m12 3-1.9 5.8L4 11l6.1 2.2L12 19l1.9-5.8L20 11l-6.1-2.2Z"/><path d="M19 3v4"/><path d="M21 5h-4"/>',
      'wrench': '<path d="M14.7 6.3a4 4 0 0 0-5.1 5.1L3 18v3h3l6.6-6.6a4 4 0 0 0 5.1-5.1l-2.8 2.8-2.1-2.1z"/>',
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

  function chatSocketNeeded() {
    var path = window.location.pathname;
    return path === '/graph' || path.indexOf('/chat/') === 0 || state.chatThinking;
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
      state.languages = s.languages || [];
      populateLanguages(state.languages);
      populateEdgeTypes(s.edge_types || {});
      el('status').textContent = 'Ready. Load a standard view or customize a slice.';
    }).catch(showError);
  }

  function populateLanguages(languages) {
    var select = el('language');
    if (!select) return;
    var current = select.value || 'all';
    var options = ['<option value="all">All</option>'];
    (languages || []).forEach(function (language) {
      options.push('<option value="' + esc(language) + '">' + esc(language) + '</option>');
    });
    select.innerHTML = options.join('');
    select.value = (current !== 'all' && (languages || []).indexOf(current) === -1) ? 'all' : current;
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
      q: el('query').value.trim(),
      language: el('language') ? el('language').value : 'all'
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
      language: el('language') ? el('language').value : 'all',
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
    var centers = languageCenters();
    var grouped = new Map();
    state.nodes.forEach(function (n) {
      var lang = n.language || 'unknown';
      if (!grouped.has(lang)) grouped.set(lang, []);
      grouped.get(lang).push(n);
    });
    state.nodes.forEach(function (n) {
      var langNodes = grouped.get(n.language || 'unknown') || state.nodes;
      var i = langNodes.indexOf(n);
      var center = centers.get(n.language || 'unknown') || {x: 0, y: 0};
      var radius = Math.max(70, Math.min(rect.width, rect.height) * (centers.size > 1 ? 0.16 : 0.34));
      var a = (Math.PI * 2 * i) / Math.max(1, langNodes.length);
      state.pos.set(n.id, {x: center.x + Math.cos(a) * radius, y: center.y + Math.sin(a) * radius});
      state.vel.set(n.id, {x: 0, y: 0});
    });
  }

  function languageCenters() {
    var selected = el('language') ? el('language').value : 'all';
    var langs = [];
    var seen = new Set();
    state.nodes.forEach(function (n) {
      var lang = n.language || 'unknown';
      if (!seen.has(lang)) {
        seen.add(lang);
        langs.push(lang);
      }
    });
    langs.sort();
    var centers = new Map();
    if (selected !== 'all' || langs.length <= 1) {
      langs.forEach(function (lang) { centers.set(lang, {x: 0, y: 0}); });
      return centers;
    }
    var rect = canvas.getBoundingClientRect();
    var spread = Math.max(260, Math.min(rect.width, rect.height) * 0.44);
    langs.forEach(function (lang, i) {
      var a = (Math.PI * 2 * i) / Math.max(1, langs.length);
      centers.set(lang, {x: Math.cos(a) * spread, y: Math.sin(a) * spread});
    });
    return centers;
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
    var centers = languageCenters();
    nodes.forEach(function (n) {
      var p = state.pos.get(n.id);
      var v = state.vel.get(n.id);
      var center = centers.get(n.language || 'unknown') || {x: 0, y: 0};
      v.x += (center.x - p.x) * 0.001;
      v.y += (center.y - p.y) * 0.001;
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
  function navigate(path, replace) {
    if (replace) window.history.replaceState({}, '', path);
    else window.history.pushState({}, '', path);
    renderRoute();
  }

  function bindRouteClick(node, path) {
    if (!node) return;
    node.addEventListener('click', function (e) {
      e.preventDefault();
      if (path === '/chat' && sessionIDRe.test(window.location.pathname.replace(/^\/chat\//, ''))) return;
      if (window.location.pathname !== path) navigate(path);
    });
  }

  function renderRoute() {
    var path = window.location.pathname;
    if (path === '/') {
      navigate('/graph', true);
      return;
    }
    var route = 'graph';
    if (path === '/settings') route = 'settings';
    else if (path === '/chat/history') route = 'history';
    else if (path === '/chat' || path.indexOf('/chat/') === 0) route = 'chat';

    document.body.dataset.route = route;
    ['graphPage', 'chatPage', 'historyPage', 'settingsPage'].forEach(function (id) {
      var node = el(id);
      if (node) node.classList.add('hidden');
    });
    el(route === 'graph' ? 'graphPage' : route === 'settings' ? 'settingsPage' : route === 'history' ? 'historyPage' : 'chatPage').classList.remove('hidden');
    el('navGraph').classList.toggle('active', route === 'graph');
    el('navChat').classList.toggle('active', route === 'chat' || route === 'history');
    el('navSettings').classList.toggle('active', route === 'settings');

    if (route === 'graph') resize();
    if (route === 'settings') loadChatProvider();
    if (route === 'history') loadChatHistory();
    if (route === 'chat') enterChatRoute(path);
  }

  var sessionIDRe = /^chat_\d+_[0-9a-f]+$/;
  function enterChatRoute(path) {
    if (path === '/chat') {
      state.chatSession = null;
      state.chatThinking = false;
      renderChat();
      renderChatThinking();
      return;
    }
    var id = path.replace(/^\/chat\//, '');
    if (id && id !== 'history' && sessionIDRe.test(id)) loadChatSession(id);
    else if (id && id !== 'history') navigate('/chat', true);
  }

  function emptyProviderConfig() {
    return {defaults: {}, providers: {supported: {}, custom: {}}, supported_models: {}};
  }

  function normalizeProviderConfig(config) {
    config = config || emptyProviderConfig();
    config.defaults = config.defaults || {};
    config.providers = config.providers || {};
    config.providers.supported = config.providers.supported || {};
    config.providers.custom = config.providers.custom || {};
    config.supported_models = config.supported_models || {};
    return config;
  }

  function loadChatProvider() {
    return api('/api/chat/provider').then(function (config) {
      state.providerConfig = normalizeProviderConfig(config);
      ensureSelectedChatModel();
      renderProviderSettings();
      renderChatModelControls();
      renderChoiceControls();
    }).catch(showError);
  }

  function saveProviderConfig(config) {
    return apiJSON('/api/chat/provider', 'PUT', config).then(function (saved) {
      state.providerConfig = normalizeProviderConfig(saved);
      ensureSelectedChatModel();
      renderProviderSettings();
      renderChatModelControls();
      return saved;
    }).catch(showError);
  }

  function cloneProviderConfig() {
    return normalizeProviderConfig(JSON.parse(JSON.stringify(state.providerConfig || emptyProviderConfig())));
  }

  function supportedProviderIDs() {
    return ['anthropic', 'openai', 'deepseek'];
  }

  function configuredProviderIDs() {
    var config = normalizeProviderConfig(state.providerConfig);
    var ids = supportedProviderIDs().filter(function (id) { return !!config.providers.supported[id]; });
    return ids.concat(Object.keys(config.providers.custom).sort());
  }

  function hasConfiguredProviders() {
    return configuredProviderIDs().length > 0;
  }

  function providerDisplayName(id) {
    var config = normalizeProviderConfig(state.providerConfig);
    if (config.supported_models[id] && config.supported_models[id].display_name) return config.supported_models[id].display_name;
    return id.replace(/[-_]/g, ' ').replace(/^./, function (c) { return c.toUpperCase(); });
  }

  function providerModels(id) {
    var config = normalizeProviderConfig(state.providerConfig);
    if (config.providers.supported[id] && config.supported_models[id]) return config.supported_models[id].models || [];
    var custom = config.providers.custom[id];
    if (!custom) return [];
    return (custom.possible_models || []).map(function (model) { return {true_name: model, display_name: model}; });
  }

  function findModel(modelName) {
    var ids = configuredProviderIDs();
    for (var i = 0; i < ids.length; i++) {
      var models = providerModels(ids[i]);
      for (var j = 0; j < models.length; j++) {
        if (models[j].true_name === modelName) return {provider: ids[i], model: models[j]};
      }
    }
    return null;
  }

  function firstAvailableModel() {
    var ids = configuredProviderIDs();
    for (var i = 0; i < ids.length; i++) {
      var models = providerModels(ids[i]);
      if (models.length) return {provider: ids[i], model: models[0]};
    }
    return null;
  }

  function ensureSelectedChatModel() {
    var config = normalizeProviderConfig(state.providerConfig);
    var selected = (state.selectedChatModel ? findModel(state.selectedChatModel) : null) || (state.chatSession && state.chatSession.model ? findModel(state.chatSession.model) : null) || findModel(config.defaults.main) || firstAvailableModel();
    state.selectedChatProvider = selected ? selected.provider : '';
    state.selectedChatModel = selected ? selected.model.true_name : '';
    if (el('chatProvider')) el('chatProvider').value = state.selectedChatProvider;
    if (el('chatModel')) el('chatModel').value = state.selectedChatModel;
  }

  function selectChatModel(provider, model) {
    state.selectedChatProvider = provider || '';
    state.selectedChatModel = model || '';
    if (el('chatProvider')) el('chatProvider').value = state.selectedChatProvider;
    if (el('chatModel')) el('chatModel').value = state.selectedChatModel;
    renderChatModelControls();
  }

  function modelLabel(modelName) {
    var found = findModel(modelName);
    return found ? found.model.display_name : (modelName || 'Select model');
  }

  function modelMenuHTML(selectedModel, providerAttr, modelAttr) {
    var ids = configuredProviderIDs();
    if (!ids.length) return '<div class="agentMenuCard"><strong>No providers</strong><small>Add a provider in Settings first.</small></div>';
    return '<div class="agentMenuCard modelMenuCard">' + ids.map(function (id) {
      var models = providerModels(id);
      if (!models.length) return '';
      return '<strong>' + esc(providerDisplayName(id)) + '</strong>' + models.map(function (model) {
        var selected = model.true_name === selectedModel ? ' selected' : '';
        return '<button class="modelOption' + selected + '" type="button" ' + providerAttr + '="' + esc(id) + '" ' + modelAttr + '="' + esc(model.true_name) + '"><span><b>' + esc(model.display_name) + '</b><small>' + esc(model.true_name) + '</small></span></button>';
      }).join('');
    }).join('') + '</div>';
  }

  function renderChatModelControls() {
    ensureSelectedChatModel();
    var button = el('chatModelButton');
    if (button) button.textContent = state.selectedChatModel ? modelLabel(state.selectedChatModel) : 'Select model';
    var menu = el('chatModelMenu');
    if (menu) menu.innerHTML = modelMenuHTML(state.selectedChatModel, 'data-model-provider', 'data-model');
    var defaultButton = el('mainModelDefaultButton');
    if (defaultButton) defaultButton.textContent = normalizeProviderConfig(state.providerConfig).defaults.main ? modelLabel(normalizeProviderConfig(state.providerConfig).defaults.main) : 'Select default model';
    var defaultMenu = el('mainModelDefaultMenu');
    if (defaultMenu) defaultMenu.innerHTML = modelMenuHTML(normalizeProviderConfig(state.providerConfig).defaults.main, 'data-default-provider', 'data-default-model');
  }

  function renderChoiceControls() {
    renderChoiceControl('chatMode', 'chatModeButton', 'chatModeMenu', {plan: 'Plan Mode', build: 'Build Mode'});
    renderChoiceControl('chatApprovalMode', 'chatApprovalModeButton', 'chatApprovalModeMenu', {manual: 'Manual Approval', auto: 'Auto Judge', always: "Don't Ask"});
  }

  function renderChoiceControl(selectID, buttonID, menuID, labels) {
    var select = el(selectID);
    var button = el(buttonID);
    var menu = el(menuID);
    if (!select || !button || !menu) return;
    button.textContent = labels[select.value] || select.value;
    menu.innerHTML = '<div class="agentMenuCard compactMenuCard">' + Object.keys(labels).map(function (value) {
      var selected = select.value === value ? ' selected' : '';
      return '<button class="modelOption' + selected + '" type="button" data-choice-select="' + esc(selectID) + '" data-choice-value="' + esc(value) + '"><span><b>' + esc(labels[value]) + '</b></span></button>';
    }).join('') + '</div>';
  }

  function openProviderEditor(type, id) {
    state.providerEditorType = type || 'supported';
    state.providerEditorID = id || null;
    var config = normalizeProviderConfig(state.providerConfig);
    var entry = id && type === 'custom' ? config.providers.custom[id] : id ? config.providers.supported[id] : null;
    var custom = state.providerEditorType === 'custom';
    el('providerEditor').classList.remove('hidden');
    el('providerChoice').value = custom ? 'custom' : (id || 'openai');
    el('providerCustomID').value = custom && id ? id : '';
    var credentialType = entry && entry.key_env ? 'env' : 'key';
    el('providerCredentialType').value = credentialType;
    el('providerCredentialValue').type = credentialType === 'env' ? 'text' : 'password';
    el('providerCredentialValue').placeholder = credentialType === 'env' ? 'OPENAI_API_KEY' : (entry && entry.key_configured ? 'saved key (leave blank to keep)' : 'API key');
    el('providerCredentialValue').value = entry && entry.key_env ? entry.key_env : '';
    el('providerBaseURL').value = entry ? (entry.base_url || '') : '';
    el('providerChatContract').value = entry ? (entry.chat_contract || 'openai') : 'openai';
    renderPossibleModels(entry && entry.possible_models ? entry.possible_models : []);
    renderProviderEditorFields();
  }

  function closeProviderEditor() {
    state.providerEditorID = null;
    el('providerEditor').classList.add('hidden');
  }

  function renderProviderEditorFields() {
    var choice = el('providerChoice').value;
    var custom = choice === 'custom';
    state.providerEditorType = custom ? 'custom' : 'supported';
    el('providerCustomWrap').classList.toggle('hidden', !custom);
    el('providerCustomFields').classList.toggle('hidden', !custom);
    el('saveProviderEntry').textContent = state.providerEditorID ? 'Save Provider' : 'Create';
    renderCredentialField();
    if (custom && !el('providerPossibleModels').children.length) renderPossibleModels([]);
  }

  function renderCredentialField() {
    var type = el('providerCredentialType').value;
    var input = el('providerCredentialValue');
    input.type = type === 'env' ? 'text' : 'password';
    input.placeholder = type === 'env' ? 'OPENAI_API_KEY' : 'API key';
  }

  function renderPossibleModels(models) {
    var list = el('providerPossibleModels');
    if (!list) return;
    list.innerHTML = (models || []).map(renderPossibleModelRow).join('');
    if (!list.innerHTML) list.innerHTML = '<div class="empty possibleModelsEmpty">No models added yet.</div>';
  }

  function renderPossibleModelRow(model) {
    return '<div class="possibleModelRow"><input value="' + esc(model || '') + '" placeholder="model true name"><button class="trashButton" type="button" data-remove-model aria-label="Remove model">' + icon('trash') + '</button></div>';
  }

  function addPossibleModel(value) {
    var list = el('providerPossibleModels');
    var empty = list.querySelector('.possibleModelsEmpty');
    if (empty) empty.remove();
    list.insertAdjacentHTML('beforeend', renderPossibleModelRow(value || ''));
    var input = list.querySelector('.possibleModelRow:last-child input');
    if (input) input.focus();
  }

  function possibleModelValues() {
    var values = [];
    el('providerPossibleModels').querySelectorAll('.possibleModelRow input').forEach(function (input) {
      var value = input.value.trim();
      if (value && values.indexOf(value) === -1) values.push(value);
    });
    return values;
  }

  function saveProviderEntry() {
    var config = cloneProviderConfig();
    var choice = el('providerChoice').value;
    var type = choice === 'custom' ? 'custom' : 'supported';
    var id = type === 'custom' ? el('providerCustomID').value.trim() : choice;
    if (!id) return showError(new Error('Provider name is required'));
    var credentialType = el('providerCredentialType').value;
    var credentialValue = el('providerCredentialValue').value.trim();
    var entry = {
      key_env: credentialType === 'env' ? credentialValue : '',
      key: credentialType === 'key' ? credentialValue : ''
    };
    if (type === 'custom') {
      entry.base_url = el('providerBaseURL').value.trim();
      entry.chat_contract = el('providerChatContract').value;
      entry.possible_models = possibleModelValues();
    }
    if (type === 'custom') {
      if (state.providerEditorID && state.providerEditorID !== id) delete config.providers.custom[state.providerEditorID];
      config.providers.custom[id] = entry;
    } else config.providers.supported[id] = entry;
    closeProviderEditor();
    return saveProviderConfig(config);
  }

  function deleteProvider(type, id) {
    if (!window.confirm('Delete provider "' + providerDisplayName(id) + '"?')) return;
    var config = cloneProviderConfig();
    if (type === 'custom') delete config.providers.custom[id];
    else delete config.providers.supported[id];
    if (config.defaults.main && !findModel(config.defaults.main)) config.defaults.main = '';
    return saveProviderConfig(config);
  }

  function renderProviderSettings() {
    var config = normalizeProviderConfig(state.providerConfig);
    var list = el('providerList');
    if (!list) return;
    var ids = configuredProviderIDs();
    list.innerHTML = ids.length ? ids.map(function (id) {
      var type = config.providers.custom[id] ? 'custom' : 'supported';
      var entry = type === 'custom' ? config.providers.custom[id] : config.providers.supported[id];
      var detail = [];
      if (entry.key_env) detail.push('env ' + entry.key_env);
      if (entry.key_configured) detail.push('saved key');
      if (entry.base_url) detail.push(entry.base_url);
      return '<div class="providerRow"><button type="button" data-provider-edit="' + esc(type) + '" data-provider-id="' + esc(id) + '"><strong>' + esc(providerDisplayName(id)) + '</strong><span>' + esc(detail.join(' / ') || 'configured') + '</span></button><button class="trashButton" type="button" data-provider-delete="' + esc(type) + '" data-provider-id="' + esc(id) + '" aria-label="Delete provider">' + icon('trash') + '</button></div>';
    }).join('') : '<div class="empty">No providers configured yet.</div>';
    el('modelDefaults').classList.toggle('hidden', !hasConfiguredProviders());
    renderChatModelControls();
  }

  function loadAgents() {
    return api('/api/chat/agents').then(function (agents) {
      state.chatAgents = Array.isArray(agents) ? agents : [];
      renderAgentMenu();
    }).catch(showError);
  }

  function loadChatSessions() {
    return api('/api/chat/sessions').then(function (sessions) {
      state.chatSessions = Array.isArray(sessions) ? sessions : [];
      renderHistoryList();
    }).catch(showError);
  }

  function loadChatHistory() {
    return loadChatSessions().then(renderHistoryList);
  }

  function createChatSession(agent, title) {
    var body = {agent: agent || 'default'};
    if (title) body.title = title;
    return apiJSON('/api/chat/sessions', 'POST', body).then(function (session) {
      state.chatSession = session;
      navigate('/chat/' + encodeURIComponent(session.id));
      return session;
    }).catch(showError);
  }

  function loadChatSession(id) {
    if (!id) return Promise.resolve();
    return api('/api/chat/sessions/' + encodeURIComponent(id)).then(function (session) {
      if (state.chatSession && state.chatSession.id !== session.id) state.chatStreaming = null;
      state.chatSession = session;
      el('chatMode').value = session.mode || 'build';
      el('chatApprovalMode').value = session.approval_mode || 'manual';
      if (session.model) selectChatModel(session.provider || state.selectedChatProvider, session.model);
      else ensureSelectedChatModel();
      renderChoiceControls();
      renderChat();
      loadChatSessions();
    }).catch(showError);
  }

  function renderChat() {
    var box = el('chatMessages');
    if (!box) return;
    var openKeys = new Set();
    box.querySelectorAll('details.tool[open]').forEach(function (d) {
      var strong = d.querySelector('summary strong');
      if (strong) openKeys.add(strong.textContent);
    });
    var session = state.chatSession;
    el('chatTitle').textContent = session ? (session.title || 'New chat') : 'New chat';
    el('chatAgentLabel').textContent = session && session.agent !== 'default' ? agentName(session.agent) + ' Task' : 'Default Chat';
    if (!session) {
      box.innerHTML = '<div class="chatWelcome"><h2>Welcome</h2><p>Ask Aracne to inspect topology, plan changes, run tools, or build features.</p></div>';
      el('chatPending').innerHTML = '';
      return;
    }
    var messages = session.messages || [];
    var html = messages.length ? messages.map(renderChatMessage).join('') : '<div class="chatWelcome"><h2>' + esc(session.title || 'New chat') + '</h2><p>This session is ready.</p></div>';
    if (state.chatStreaming && state.chatStreaming.sessionID === session.id && (state.chatStreaming.content || state.chatStreaming.reasoning)) {
      html += renderChatStreaming();
    }
    box.innerHTML = html;
    if (openKeys.size) {
      box.querySelectorAll('details.tool').forEach(function (d) {
        var strong = d.querySelector('summary strong');
        if (strong && openKeys.has(strong.textContent)) d.open = true;
      });
    }
    box.scrollTop = box.scrollHeight;
    renderChatPending();
  }

  function formatToolInput(obj) {
    if (!obj) return '';
    var parts = [];
    for (var key in obj) {
      var val = obj[key];
      var str;
      if (typeof val === 'string') {
        str = key + '="' + val + '"';
      } else if (typeof val === 'boolean' || typeof val === 'number') {
        str = key + '=' + val;
      } else {
        str = key + '=' + JSON.stringify(val);
      }
      parts.push(str);
    }
    return parts.join(' ');
  }
  function renderChatMessage(msg) {
    var role = msg.role || 'assistant';
    var status = msg.status || '';
    if (role === 'tool') {
      var input = msg.tool_input ? JSON.stringify(msg.tool_input, null, 2) : '{}';
      var output = msg.tool_output || '';
      var inputStr = formatToolInput(msg.tool_input);
      var displayName = esc(msg.tool_name || 'tool');
      if (inputStr) {
        var maxLen = 60;
        if (inputStr.length > maxLen) {
          inputStr = inputStr.substring(0, maxLen) + '...';
        }
        displayName += ' ' + esc(inputStr);
      }
      return '<details class="chatMessage tool ' + esc(status) + '">' +
        '<summary class="toolCallSummary"><strong>' + displayName + '</strong><span>' + esc(status || 'pending') + '</span></summary>' +
        '<div class="toolDetails"><label>Input</label><pre>' + esc(input) + '</pre><label>Output</label><div class="toolDetailsMarkdown">' + renderMarkdown(output) + '</div></div>' +
        '</details>';
    }
    return '<div class="chatMessage ' + esc(role) + ' ' + esc(status) + '">' +
      '<span class="chatRole">' + esc(role) + '</span>' +
      (msg.reasoning ? '<div class="reasoningBlock"><label>Reasoning</label><div>' + esc(msg.reasoning) + '</div></div>' : '') +
      '<div class="messageMarkdown">' + renderMarkdown(msg.content || '') + '</div>' +
      '</div>';
  }

  function renderChatStreaming() {
    return renderChatMessage({
      role: 'assistant',
      status: 'streaming',
      content: state.chatStreaming.content,
      reasoning: state.chatStreaming.reasoning
    });
  }

  function renderChatPending() {
    var session = state.chatSession;
    var panel = el('chatPending');
    if (!panel || !session) return;
    var html = [];
    var workflowEvents = (session.events || []).filter(function (event) {
      return event.type && event.type.indexOf('workflow_') === 0;
    }).slice(-8);
    workflowEvents.forEach(function (event) {
      var payload = event.payload || {};
      var label = (payload.type ? agentName(payload.type) : 'Task') + ' ' + event.type.replace('workflow_', '').replace(/_/g, ' ');
      var detail = '';
      if (payload.total) detail = 'Batch ' + esc(payload.completed || 0) + ' of ' + esc(payload.total);
      else if (payload.queued !== undefined) detail = esc(payload.queued) + ' item(s) queued';
      if (payload.error) detail += (detail ? ' - ' : '') + esc(payload.error);
      html.push('<div class="pendingCard"><strong>' + esc(label) + '</strong>' + (detail ? '<p>' + detail + '</p>' : '') + (payload.text ? '<pre>' + esc(payload.text) + '</pre>' : '') + '</div>');
    });
    (session.pending_approvals || []).forEach(function (approval) {
      html.push('<div class="pendingCard"><strong>Approve tool call?</strong><p>' + esc(approval.reason || '') + '</p><code>' + esc(approval.tool_call && approval.tool_call.function ? approval.tool_call.function.name : '') + '</code><div class="pendingActions"><button data-approval="' + esc(approval.id) + '" data-approved="true" type="button">Allow</button><button data-approval="' + esc(approval.id) + '" data-approved="false" type="button">Decline</button></div></div>');
    });
    (session.pending_questions || []).forEach(function (question) {
      var options = (question.options || []).map(function (option) { return '<button data-question="' + esc(question.id) + '" data-answer="' + esc(option) + '" type="button">' + esc(option) + '</button>'; }).join('');
      html.push('<div class="pendingCard"><strong>Question</strong><p>' + esc(question.question || '') + '</p><div class="pendingActions">' + options + '</div><form class="questionForm" data-question="' + esc(question.id) + '"><input placeholder="Answer"><button type="submit">Answer</button></form></div>');
    });
    panel.innerHTML = html.join('');
  }

  function sendChatMessage(e) {
    e.preventDefault();
    var input = el('chatInput');
    var content = input.value.trim();
    if (!content) return;
    ensureSelectedChatModel();
    var payload = {
      content: content,
      mode: el('chatMode').value,
      approval_mode: el('chatApprovalMode').value,
      provider: {provider: state.selectedChatProvider, model: state.selectedChatModel}
    };
    state.chatThinking = true;
    state.chatStreaming = null;
    renderChatThinking();
    function sent(session) {
      input.value = '';
      state.chatSession = session;
      renderChat();
      loadChatSessions();
    }
    function failed(err) {
      state.chatThinking = false;
      input.value = content;
      renderChatThinking();
      showChatError(err);
    }
    if (!state.chatSession) {
      return apiJSON('/api/chat/sessions', 'POST', payload).then(function (session) {
        sent(session);
        navigate('/chat/' + encodeURIComponent(session.id));
      }).catch(failed);
    }
    return apiJSON('/api/chat/sessions/' + encodeURIComponent(state.chatSession.id) + '/messages', 'POST', payload).then(sent).catch(failed);
  }

  function handleChatEvent(event) {
    if (!event || !event.type) return;
    if (event.type === 'delta' && state.chatSession && event.session_id === state.chatSession.id) {
      var payload = event.payload || {};
      if (!state.chatStreaming || state.chatStreaming.sessionID !== event.session_id) {
        state.chatStreaming = {sessionID: event.session_id, content: '', reasoning: ''};
      }
      state.chatStreaming.content += payload.content || '';
      state.chatStreaming.reasoning += payload.reasoning || '';
      state.chatThinking = true;
      renderChat();
      renderChatThinking();
      return;
    }
    if (event.type === 'thinking' && state.chatSession && event.session_id === state.chatSession.id) {
      state.chatThinking = !!(event.payload && event.payload.active);
      renderChatThinking();
      return;
    }
    if ((event.type === 'message' || event.type === 'error') && state.chatSession && event.session_id === state.chatSession.id) {
      state.chatStreaming = null;
    }
    if ((event.type === 'workflow_completed' || event.type === 'workflow_failed') && state.chatSession && event.session_id === state.chatSession.id) {
      state.chatThinking = false;
      renderChatThinking();
    }
    if (event.type === 'session_title_updated') loadChatSessions();
    if (state.chatSession && event.session_id === state.chatSession.id) loadChatSession(state.chatSession.id);
    else loadChatSessions();
  }

  function renderChatThinking() {
    var thinking = el('chatThinking');
    if (thinking) thinking.classList.toggle('hidden', !state.chatThinking);
  }

  function resolveApproval(approvalID, approved) {
    if (!state.chatSession) return;
    apiJSON('/api/chat/sessions/' + encodeURIComponent(state.chatSession.id) + '/approvals/' + encodeURIComponent(approvalID), 'POST', {approved: approved}).then(function (session) {
      state.chatSession = session;
      renderChat();
    }).catch(showError);
  }

  function answerQuestion(questionID, answer) {
    if (!state.chatSession) return;
    apiJSON('/api/chat/sessions/' + encodeURIComponent(state.chatSession.id) + '/questions/' + encodeURIComponent(questionID), 'POST', {answer: answer}).then(function (session) {
      state.chatSession = session;
      renderChat();
    }).catch(showError);
  }

  function renderHistoryList() {
    var list = el('chatHistoryList');
    if (!list) return;
    if (!state.chatSessions.length) {
      list.innerHTML = '<div class="empty">No chats yet.</div>';
      return;
    }
    list.innerHTML = state.chatSessions.map(function (session) {
      return '<a class="historyItem" href="/chat/' + esc(session.id) + '" data-route="/chat/' + esc(session.id) + '"><strong>' + esc(session.title || 'New chat') + '</strong><span>' + esc(agentName(session.agent)) + ' · ' + esc(session.updated_at || '') + '</span></a>';
    }).join('');
  }

  function renderAgentMenu() {
    var menu = el('agentMenu');
    if (!menu) return;
    menu.innerHTML = '<div class="agentMenuCard"><strong>Start task</strong>' + state.chatAgents.map(function (agent) {
      return '<button type="button" data-task="' + esc(agent.id) + '">' + icon(agent.icon || 'bot') + '<span><b>' + esc(agent.name) + '</b><small>' + esc(agent.description || '') + '</small></span></button>';
    }).join('') + '</div>';
  }

  function positionMenu(button, menu) {
    var rect = button.getBoundingClientRect();
    menu.style.left = rect.left + 'px';
    menu.style.bottom = (window.innerHeight - rect.top + 8) + 'px';
  }

  function agentName(id) {
    id = id || 'default';
    if (id === 'default') return 'Default Chat';
    var agent = (state.chatAgents || []).find(function (item) { return item.id === id; });
    return agent ? agent.name : id.replace(/_/g, ' ').replace(/^./, function (c) { return c.toUpperCase(); });
  }

  function startTask(taskID) {
    var name = agentName(taskID);
    if (!window.confirm('Start the ' + name + ' task?')) return;
    el('agentMenu').classList.add('hidden');
    state.chatThinking = true;
    renderChatThinking();
    createChatSession('default', name).then(function (session) {
      if (!session) return;
      return apiJSON('/api/chat/workflows', 'POST', {session_id: session.id, type: taskID, batch_size: 5, parallel: 2}).catch(showChatError);
    });
  }

  function startWorkflow(kind) {
    function run() {
      return apiJSON('/api/chat/workflows', 'POST', {session_id: state.chatSession.id, type: kind, batch_size: 5, parallel: 2}).catch(showChatError);
    }
    if (state.chatSession) run(); else createChatSession('default', agentName(kind)).then(run);
  }

  function showChatError(err) {
    showError(err);
    var panel = el('chatPending');
    if (panel) panel.innerHTML = '<div class="pendingCard error"><strong>Chat request failed</strong><p>' + esc(err.message || err) + '</p></div>';
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
    state.dragMoved = false;
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
    if (Math.abs(e.clientX - state.dragStart.x) > 3 || Math.abs(e.clientY - state.dragStart.y) > 3) state.dragMoved = true;
    state.ox = state.dragStart.ox + e.clientX - state.dragStart.x;
    state.oy = state.dragStart.oy + e.clientY - state.dragStart.y;
    draw();
  });
  canvas.addEventListener('click', function (e) {
    if (state.dragMoved) return;
    var n = nearest(e.clientX, e.clientY);
    if (n) selectNode(n.id);
    else loadGraph();
  });
  canvas.addEventListener('dblclick', function (e) {
    var n = nearest(e.clientX, e.clientY);
    if (n) loadNeighborhood(n.id);
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
  el('language').addEventListener('change', loadGraph);
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
  bindRouteClick(document.querySelector('.appBrand'), '/graph');
  bindRouteClick(el('navGraph'), '/graph');
  bindRouteClick(el('navChat'), '/chat');
  bindRouteClick(el('navSettings'), '/settings');
  el('chatHistoryList').addEventListener('click', function (e) {
    var routeLink = e.target.closest('[data-route]');
    if (!routeLink || !el('chatHistoryList').contains(routeLink)) return;
    e.preventDefault();
    navigate(routeLink.getAttribute('href') || routeLink.dataset.route);
  });
  document.addEventListener('click', function (e) {
    if (!e.target.closest('.multiPicker')) closeMultiPickers();
    document.querySelectorAll('.agentMenu:not(.hidden)').forEach(function (menu) {
      var buttonID = menu.id.replace('Menu', 'Button');
      var button = document.getElementById(buttonID);
      if (!menu.contains(e.target) && (!button || !button.contains(e.target))) {
        menu.classList.add('hidden');
      }
    });
  });
  window.addEventListener('popstate', renderRoute);
  el('addProvider').onclick = function () { openProviderEditor('supported'); };
  el('cancelProviderEntry').onclick = closeProviderEditor;
  el('saveProviderEntry').onclick = saveProviderEntry;
  el('providerChoice').onchange = renderProviderEditorFields;
  el('providerCredentialType').onchange = renderCredentialField;
  el('addPossibleModel').onclick = function () { addPossibleModel(''); };
  el('providerPossibleModels').addEventListener('click', function (e) {
    var remove = e.target.closest('[data-remove-model]');
    if (!remove) return;
    remove.closest('.possibleModelRow').remove();
    if (!el('providerPossibleModels').querySelector('.possibleModelRow')) renderPossibleModels([]);
  });
  el('providerList').addEventListener('click', function (e) {
    var edit = e.target.closest('[data-provider-edit]');
    if (edit) {
      openProviderEditor(edit.dataset.providerEdit, edit.dataset.providerId);
      return;
    }
    var del = e.target.closest('[data-provider-delete]');
    if (del) deleteProvider(del.dataset.providerDelete, del.dataset.providerId);
  });
  el('chatModelButton').onclick = function () { positionMenu(this, el('chatModelMenu')); el('chatModelMenu').classList.toggle('hidden'); };
  el('chatModelMenu').addEventListener('click', function (e) {
    var option = e.target.closest('[data-model]');
    if (!option) return;
    selectChatModel(option.dataset.modelProvider, option.dataset.model);
    el('chatModelMenu').classList.add('hidden');
  });
  el('mainModelDefaultButton').onclick = function () { el('mainModelDefaultMenu').classList.toggle('hidden'); };
  el('mainModelDefaultMenu').addEventListener('click', function (e) {
    var option = e.target.closest('[data-default-model]');
    if (!option) return;
    var config = cloneProviderConfig();
    config.defaults.main = option.dataset.defaultModel;
    el('mainModelDefaultMenu').classList.add('hidden');
    saveProviderConfig(config);
  });
  ['chatMode', 'chatApprovalMode'].forEach(function (selectID) {
    var buttonID = selectID === 'chatMode' ? 'chatModeButton' : 'chatApprovalModeButton';
    var menuID = selectID === 'chatMode' ? 'chatModeMenu' : 'chatApprovalModeMenu';
    el(buttonID).onclick = function () { positionMenu(this, el(menuID)); el(menuID).classList.toggle('hidden'); };
    el(menuID).addEventListener('click', function (e) {
      var choice = e.target.closest('[data-choice-value]');
      if (!choice) return;
      el(choice.dataset.choiceSelect).value = choice.dataset.choiceValue;
      el(menuID).classList.add('hidden');
      renderChoiceControls();
    });
  });
  el('chatInput').addEventListener('keydown', function (e) {
    if (e.key === 'Enter' && !e.shiftKey && !e.ctrlKey && !e.metaKey) {
      e.preventDefault();
      el('chatComposer').requestSubmit();
    }
  });
  el('chatComposer').addEventListener('submit', sendChatMessage);
  document.querySelector('.sendButton').addEventListener('click', function (e) {
    e.preventDefault();
    el('chatComposer').requestSubmit();
  });
  el('historyButton').onclick = function () { navigate('/chat/history'); };
  el('newChatFromHistory').onclick = function () { navigate('/chat'); };
  el('agentButton').onclick = function () {
    positionMenu(this, el('agentMenu'));
    el('agentMenu').classList.toggle('hidden');
  };
  el('agentMenu').addEventListener('click', function (e) {
    var button = e.target.closest('[data-task]');
    if (button) startTask(button.dataset.task);
  });
  el('chatPending').addEventListener('click', function (e) {
    var approval = e.target.closest('[data-approval]');
    if (approval) {
      resolveApproval(approval.dataset.approval, approval.dataset.approved === 'true');
      return;
    }
    var question = e.target.closest('[data-question][data-answer]');
    if (question) answerQuestion(question.dataset.question, question.dataset.answer);
  });
  el('chatPending').addEventListener('submit', function (e) {
    var form = e.target.closest('.questionForm');
    if (!form) return;
    e.preventDefault();
    var input = form.querySelector('input');
    answerQuestion(form.dataset.question, input ? input.value : '');
  });
  loadChatProvider();
  loadAgents().then(loadChatSessions).then(renderRoute);
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
  Promise.all([loadOptimizationRules()]).then(loadGraph);
})();
