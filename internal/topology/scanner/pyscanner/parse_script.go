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

def var_def(arg):
    annot = expr_str(arg.annotation) if arg.annotation else ''
    return {'name': arg.arg, 'typing': annot}

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
    # Like ast.walk, but does not descend into nested function/class/lambda
    # scopes, so an inner function's calls are not attributed to its parent.
    todo = [node]
    i = 0
    while i < len(todo):
        cur = todo[i]
        i += 1
        yield cur
        for child in ast.iter_child_nodes(cur):
            if isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda, ast.ClassDef)):
                continue
            todo.append(child)

def extract_body_calls(body):
    calls = []
    for stmt in body:
        if isinstance(stmt, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            continue
        for node in walk_no_nested_scopes(stmt):
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute):
                if isinstance(node.func.value, ast.Name):
                    calls.append({
                        'object_name': node.func.value.id,
                        'method_name': node.func.attr,
                        'func': expr_str(node.func),
                        'lineno': node.lineno,
                    })
            elif isinstance(node, ast.Call) and isinstance(node.func, ast.Name):
                calls.append({
                    'object_name': '',
                    'method_name': '',
                    'func': node.func.id,
                    'lineno': node.lineno,
                })
    return calls

def extract_local_assignments(body):
    assignments = []
    for stmt in body:
        t = type(stmt).__name__
        if t == 'Assign':
            for target in stmt.targets:
                if isinstance(target, ast.Name):
                    v = expr_str(stmt.value)
                    assignments.append({
                        'name': target.id,
                        'value_type': v,
                        'lineno': stmt.lineno,
                    })
        elif t == 'AnnAssign':
            if stmt.target and isinstance(stmt.target, ast.Name):
                v = expr_str(stmt.value) if stmt.value else ''
                assignments.append({
                    'name': stmt.target.id,
                    'value_type': expr_str(stmt.annotation),
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
                    'lineno': item.lineno,
                    'end_lineno': getattr(item, 'end_lineno', item.lineno),
                })
        elif t == 'Assign':
            v = expr_str(item.value) if item.value else ''
            for target in item.targets:
                if type(target).__name__ == 'Name':
                    variables.append({
                        'name': target.id,
                        'typing': '',
                        'value': v,
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
        results.append({'name': '', 'typing': expr_str(node.returns)})
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
    }

def parse_class(node, import_map):
    bases = [expr_str(b) for b in node.bases]
    decs = decorator_names(node)
    base_str = ' '.join(bases)
    is_abc = any(n in base_str for n in ('ABC', 'ABCMeta'))
    is_protocol = 'Protocol' in base_str

    funcs, subclasses, vars_ = extract_body(node.body, node.name, import_map)
    has_abstract = any(f.get('is_abstract') for f in funcs)

    return {
        'name': node.name,
        'docstring': ast.get_docstring(node) or '',
        'bases': bases,
        'decorators': decs,
        'methods': funcs,
        'class_vars': vars_,
        'is_abc': is_abc,
        'is_protocol': is_protocol,
        'has_abstract_methods': has_abstract,
        'lineno': node.lineno,
        'end_lineno': getattr(node, 'end_lineno', node.lineno),
    }

def parse_file(path, module_root):
    with open(path, 'r', encoding='utf-8') as f:
        source = f.read()
    tree = ast.parse(source, filename=path)

    import_map = {}
    imports = []
    for node in ast.iter_child_nodes(tree):
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
