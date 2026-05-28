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
    if t == 'Call': return 'callable'
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
    for d in decorators:
        if d in ('abstractmethod', 'abc.abstractmethod'):
            return True
    return False

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
                    'value': v
                })
        elif t == 'Assign':
            v = expr_str(item.value) if item.value else ''
            for target in item.targets:
                if type(target).__name__ == 'Name':
                    variables.append({
                        'name': target.id,
                        'typing': '',
                        'value': v
                    })
        elif t == 'Expr':
            pass
    return functions, classes, variables

def parse_func(node, parent, import_map):
    decs = decorator_names(node)
    is_prop = any('property' in d or d.endswith('.setter') for d in decs)
    params = []
    for attr_name in ('args', 'posonlyargs', 'kwonlyargs'):
        for a in getattr(getattr(node, 'args'), attr_name, []):
            if hasattr(a, 'arg'):
                params.append(var_def(a))
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
            for alias in node.names:
                full = (mod + '.' + alias.name) if mod else alias.name
                key = alias.asname or alias.name
                import_map[key] = full
                imports.append({'name': full, 'alias': key, 'module': mod})

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
