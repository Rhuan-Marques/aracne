package pyscanner

const pythonParseScript = `
import ast, json, sys, os

def expr_str(node):
    if node is None: return ''
    t = type(node).__name__
    if t == 'Name': return node.id
    if t == 'Attribute': return expr_str(node.value) + '.' + node.attr
    if t == 'Constant':
        v = node.value
        if v is None: return 'None'
        if isinstance(v, str): return repr(v)
        return str(v)
    if t == 'Subscript': return expr_str(node.value)
    if t == 'Starred': return '*' + expr_str(node.value)
    if t == 'List': return 'list'
    if t == 'Tuple': return 'tuple'
    if t == 'Dict': return 'dict'
    if t == 'Set': return 'set'
    if t == 'Call': return expr_str(node.func)
    if t == 'BinOp': return 'expr'
    if t == 'UnaryOp': return expr_str(node.operand)
    if t == 'JoinedStr': return 'str'
    return t

def annot_refs(node, out=None):
    # Collect candidate type names referenced by a type annotation, descending
    # through subscripts, PEP 604 unions, and tuple/list parameter groups so
    # every inner class survives (Optional[X], Union[A,B], A | B, list[X],
    # dict[k,V], Callable[[A],B]). Quoted forward references are re-parsed and
    # resolved; Literal[...] holds values, not types, so its slice is skipped.
    if out is None:
        out = []
    if node is None:
        return out
    t = type(node).__name__
    if t == 'Name':
        out.append(node.id)
    elif t == 'Attribute':
        out.append(expr_str(node))
    elif t == 'Constant':
        if isinstance(node.value, str):
            try:
                annot_refs(ast.parse(node.value, mode='eval').body, out)
            except Exception:
                pass
    elif t == 'Subscript':
        annot_refs(node.value, out)
        if expr_str(node.value).split('.')[-1] != 'Literal':
            annot_refs(node.slice, out)
    elif t == 'Index':
        annot_refs(node.value, out)
    elif t in ('Tuple', 'List'):
        for e in node.elts:
            annot_refs(e, out)
    elif t == 'BinOp':
        annot_refs(node.left, out)
        annot_refs(node.right, out)
    return out

def alias_refs(value_node):
    # A module-level subscript (Name[...]) or PEP 604 union (A | B) reads like a
    # type alias; collect its inner type names so an annotation using the alias
    # resolves transitively to the aliased classes.
    if value_node is None:
        return []
    if type(value_node).__name__ in ('Subscript', 'BinOp'):
        return annot_refs(value_node)
    return []

def var_def(arg):
    annot = expr_str(arg.annotation) if arg.annotation else ''
    return {'name': arg.arg, 'typing': annot, 'type_refs': annot_refs(arg.annotation)}

def decorator_names(node):
    return [expr_str(d) for d in getattr(node, 'decorator_list', [])]

def is_abstract(decorators):
    abstract_names = ('abstractmethod', 'abstractproperty',
                      'abstractclassmethod', 'abstractstaticmethod')
    for d in decorators:
        if d.split('.')[-1] in abstract_names:
            return True
    return False

def walk_no_nested_scopes(node):
    # Like ast.walk, but does not descend into nested function/class scopes, so
    # an inner function's calls are not attributed to its parent. Lambdas ARE
    # descended into: they are anonymous, so their calls belong to the enclosing
    # function (e.g. fn = lambda v: helper(v) -> the parent calls helper).
    todo = [node]
    i = 0
    while i < len(todo):
        cur = todo[i]
        i += 1
        yield cur
        for child in ast.iter_child_nodes(cur):
            if isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
                continue
            todo.append(child)

# Maps a binary-operator AST node type to the dunder method it invokes, so
# a + b is recorded as a call to a.__add__ when a is typed to a class.
BINOP_DUNDERS = {
    'Add': '__add__', 'Sub': '__sub__', 'Mult': '__mul__',
    'MatMult': '__matmul__', 'Div': '__truediv__', 'FloorDiv': '__floordiv__',
    'Mod': '__mod__', 'Pow': '__pow__', 'LShift': '__lshift__',
    'RShift': '__rshift__', 'BitOr': '__or__', 'BitXor': '__xor__',
    'BitAnd': '__and__',
}

def extract_body_calls(body):
    calls = []
    for stmt in body:
        if isinstance(stmt, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            continue
        for node in walk_no_nested_scopes(stmt):
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute):
                val = node.func.value
                if isinstance(val, ast.Name):
                    calls.append({
                        'object_name': val.id,
                        'method_name': node.func.attr,
                        'func': expr_str(node.func),
                        'lineno': node.lineno,
                    })
                elif (isinstance(val, ast.Call) and isinstance(val.func, ast.Name)
                      and val.func.id == 'super'):
                    # super().method(...): the 'super' object_name is a marker the
                    # Go resolver resolves against the receiver class's bases.
                    calls.append({
                        'object_name': 'super',
                        'method_name': node.func.attr,
                        'func': 'super().' + node.func.attr,
                        'lineno': node.lineno,
                    })
            elif isinstance(node, ast.Call) and isinstance(node.func, ast.Name):
                calls.append({
                    'object_name': '',
                    'method_name': '',
                    'func': node.func.id,
                    'lineno': node.lineno,
                })
            elif isinstance(node, ast.BinOp) and isinstance(node.left, ast.Name):
                # a + b -> a.__add__(b): record the operator as a call so an
                # overloaded dunder on a typed operand resolves to a Calls edge.
                dunder = BINOP_DUNDERS.get(type(node.op).__name__)
                if dunder:
                    calls.append({
                        'object_name': node.left.id,
                        'method_name': dunder,
                        'func': node.left.id + '.' + dunder,
                        'lineno': getattr(node, 'lineno', 0),
                    })
            elif (isinstance(node, ast.Subscript) and isinstance(node.value, ast.Name)
                  and isinstance(getattr(node, 'ctx', None), ast.Load)):
                # a[i] (read) -> a.__getitem__(i).
                calls.append({
                    'object_name': node.value.id,
                    'method_name': '__getitem__',
                    'func': node.value.id + '.__getitem__',
                    'lineno': getattr(node, 'lineno', 0),
                })
            elif type(node).__name__ == 'MatchClass':
                # A 'case ClassName(...)' pattern references the class as a use;
                # record the class name (as a bare reference) so the resolver
                # can emit a uses_class edge.
                calls.append({
                    'object_name': '',
                    'method_name': '',
                    'func': expr_str(node.cls),
                    'lineno': getattr(node, 'lineno', 0),
                })
    return calls

def extract_local_assignments(body):
    assignments = []
    for stmt in body:
        t = type(stmt).__name__
        if t == 'Assign':
            op_left = ''
            op_dunder = ''
            if isinstance(stmt.value, ast.BinOp) and isinstance(stmt.value.left, ast.Name):
                op_dunder = BINOP_DUNDERS.get(type(stmt.value.op).__name__, '')
                if op_dunder:
                    op_left = stmt.value.left.id
            for target in stmt.targets:
                if isinstance(target, ast.Name):
                    v = expr_str(stmt.value)
                    assignments.append({
                        'name': target.id,
                        'value_type': v,
                        'lineno': stmt.lineno,
                        'op_left': op_left,
                        'op_dunder': op_dunder,
                    })
        elif t == 'AnnAssign':
            if stmt.target and isinstance(stmt.target, ast.Name):
                v = expr_str(stmt.value) if stmt.value else ''
                assignments.append({
                    'name': stmt.target.id,
                    'value_type': expr_str(stmt.annotation),
                    'lineno': stmt.lineno,
                })
        elif t in ('With', 'AsyncWith'):
            # 'with X() as h' / 'async with X() as h' binds h to an instance of
            # X; record it like a local assignment so h.method() resolves.
            for item in getattr(stmt, 'items', []):
                var = getattr(item, 'optional_vars', None)
                if isinstance(var, ast.Name):
                    assignments.append({
                        'name': var.id,
                        'value_type': expr_str(item.context_expr),
                        'lineno': stmt.lineno,
                    })
    return assignments

def extract_body(body, parent, import_map):
    functions = []
    classes = []
    variables = []
    for item in body:
        t = type(item).__name__
        if t == 'FunctionDef':
            functions.append(parse_func(item, parent, import_map))
        elif t == 'AsyncFunctionDef':
            f = parse_func(item, parent, import_map)
            f['is_async'] = True
            functions.append(f)
        elif t == 'ClassDef':
            classes.append(parse_class(item, import_map))
        elif t == 'AnnAssign':
            if item.target and type(item.target).__name__ == 'Name':
                v = expr_str(item.value) if item.value else ''
                variables.append({
                    'name': item.target.id,
                    'typing': expr_str(item.annotation),
                    'value': v,
                    'type_refs': alias_refs(item.value),
                    'lineno': item.lineno,
                    'end_lineno': getattr(item, 'end_lineno', item.lineno),
                })
        elif t == 'Assign':
            v = expr_str(item.value) if item.value else ''
            refs = alias_refs(item.value)
            for target in item.targets:
                if type(target).__name__ == 'Name':
                    variables.append({
                        'name': target.id,
                        'typing': '',
                        'value': v,
                        'type_refs': refs,
                        'lineno': item.lineno,
                        'end_lineno': getattr(item, 'end_lineno', item.lineno),
                    })
        elif t == 'TypeAlias':
            # PEP 695 'type X = ...': emit X as a resource and record the inner
            # types it aliases so annotations using X resolve transitively.
            name = item.name.id if type(item.name).__name__ == 'Name' else expr_str(item.name)
            variables.append({
                'name': name,
                'typing': '',
                'value': expr_str(item.value),
                'type_refs': annot_refs(item.value),
                'lineno': item.lineno,
                'end_lineno': getattr(item, 'end_lineno', item.lineno),
            })
        elif t in ('If', 'Try', 'With', 'AsyncWith', 'For', 'AsyncFor', 'While'):
            nested_bodies = [getattr(item, 'body', []),
                             getattr(item, 'orelse', []),
                             getattr(item, 'finalbody', [])]
            for handler in getattr(item, 'handlers', []):
                nested_bodies.append(getattr(handler, 'body', []))
            for nb in nested_bodies:
                if nb:
                    nf, nc, nv = extract_body(nb, parent, import_map)
                    functions.extend(nf)
                    classes.extend(nc)
                    variables.extend(nv)
        elif t == 'Expr':
            pass
    return functions, classes, variables

def doc_positions(node):
    # Returns (first_body_statement_line, docstring_start, docstring_end). The
    # docstring lines are 0 when the node has no docstring. Used by apply to insert
    # or replace a docstring as the first body statement.
    body = getattr(node, 'body', [])
    if not body:
        return 0, 0, 0
    first = body[0]
    body_lineno = getattr(first, 'lineno', 0)
    doc_start = 0
    doc_end = 0
    if ast.get_docstring(node) is not None:
        doc_start = body_lineno
        doc_end = getattr(first, 'end_lineno', body_lineno)
    return body_lineno, doc_start, doc_end

def parse_func(node, parent, import_map):
    decs = decorator_names(node)
    is_prop = any(d in ('property', 'cached_property', 'functools.cached_property')
                  or d.endswith('.setter') or d.endswith('.getter') for d in decs)
    params = []
    for attr_name in ('posonlyargs', 'args', 'kwonlyargs'):
        for a in getattr(getattr(node, 'args'), attr_name, []):
            if hasattr(a, 'arg'):
                params.append(var_def(a))
    vararg = getattr(node.args, 'vararg', None)
    if vararg is not None:
        params.append(var_def(vararg))
    kwarg = getattr(node.args, 'kwarg', None)
    if kwarg is not None:
        params.append(var_def(kwarg))
    results = []
    if getattr(node, 'returns', None):
        results.append({'name': '', 'typing': expr_str(node.returns),
                        'type_refs': annot_refs(node.returns)})
    body_lineno, doc_start, doc_end = doc_positions(node)
    return {
        'name': node.name,
        'docstring': ast.get_docstring(node) or '',
        'decorators': decs,
        'is_async': type(node).__name__ == 'AsyncFunctionDef',
        'is_property': is_prop,
        'is_abstract': is_abstract(decs),
        'params': params,
        'results': results,
        'lineno': node.lineno,
        'end_lineno': getattr(node, 'end_lineno', node.lineno),
        'parent': parent,
        'body_calls': extract_body_calls(node.body),
        'body_assignments': extract_local_assignments(node.body),
        'body_lineno': body_lineno,
        'doc_start': doc_start,
        'doc_end': doc_end,
    }

def parse_class(node, import_map):
    bases = [expr_str(b) for b in node.bases]
    decs = decorator_names(node)
    base_str = ' '.join(bases)
    is_abc = any(n in base_str for n in ('ABC', 'ABCMeta'))
    is_protocol = 'Protocol' in base_str

    # metaclass=Meta is a class keyword, not a base; record it so the resolver
    # can emit a uses_class edge to the metaclass.
    metaclass = ''
    for kw in getattr(node, 'keywords', []):
        if kw.arg == 'metaclass':
            metaclass = expr_str(kw.value)

    funcs, subclasses, vars_ = extract_body(node.body, node.name, import_map)
    has_abstract = any(f.get('is_abstract') for f in funcs)
    body_lineno, doc_start, doc_end = doc_positions(node)

    return {
        'name': node.name,
        'docstring': ast.get_docstring(node) or '',
        'bases': bases,
        'decorators': decs,
        'methods': funcs,
        'class_vars': vars_,
        'nested_classes': subclasses,
        'metaclass': metaclass,
        'is_abc': is_abc,
        'is_protocol': is_protocol,
        'has_abstract_methods': has_abstract,
        'lineno': node.lineno,
        'end_lineno': getattr(node, 'end_lineno', node.lineno),
        'body_lineno': body_lineno,
        'doc_start': doc_start,
        'doc_end': doc_end,
    }

def parse_file(path, module_root):
    with open(path, 'r', encoding='utf-8') as f:
        source = f.read()
    tree = ast.parse(source, filename=path)

    import_map = {}
    imports = []

    def record_import(node):
        t = type(node).__name__
        if t == 'Import':
            for alias in node.names:
                key = alias.asname or alias.name.split('.')[0]
                import_map[key] = alias.name
                imports.append({'name': alias.name, 'alias': key})
        elif t == 'ImportFrom':
            mod = node.module or ''
            level = getattr(node, 'level', 0) or 0
            for alias in node.names:
                full = (mod + '.' + alias.name) if mod else alias.name
                key = alias.asname or alias.name
                import_map[key] = full
                imports.append({'name': full, 'alias': key, 'module': mod, 'level': level})

    def collect_imports(body):
        # Module-level imports, including those guarded by try/except or if/else
        # (optional-dependency fallbacks). Descends through control-flow blocks
        # but NOT into function/class bodies, whose imports are local, not module
        # dependencies.
        for node in body:
            t = type(node).__name__
            if t in ('Import', 'ImportFrom'):
                record_import(node)
            elif t in ('If', 'Try', 'With', 'AsyncWith', 'For', 'AsyncFor', 'While'):
                for nb in (getattr(node, 'body', []),
                           getattr(node, 'orelse', []),
                           getattr(node, 'finalbody', [])):
                    collect_imports(nb)
                for handler in getattr(node, 'handlers', []):
                    collect_imports(getattr(handler, 'body', []))

    collect_imports(tree.body)

    relevant_nodes = [n for n in ast.iter_child_nodes(tree)
                      if type(n).__name__ not in ('Import', 'ImportFrom')]

    funcs, classes, vars_ = extract_body(relevant_nodes, None, import_map)

    return {
        'docstring': ast.get_docstring(tree) or '',
        'imports': imports,
        'import_map': {k: v for k, v in import_map.items()},
        'functions': funcs,
        'classes': classes,
        'variables': vars_,
    }

if __name__ == '__main__':
    path = sys.argv[1]
    root = sys.argv[2] if len(sys.argv) > 2 else os.path.dirname(path)
    result = parse_file(path, root)
    json.dump(result, sys.stdout)
`
