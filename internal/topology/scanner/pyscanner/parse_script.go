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

def annot_str(node):
    # An annotation AS WRITTEN. expr_str collapses composites -- a subscript to its
    # head, a union to the placeholder 'expr' -- which is right for a value expression
    # (the class an assignment produces) and wrong for a type: 'int | None' became
    # 'expr' and 'typing.Sequence[str]' became 'typing.Sequence', so the matcher
    # compared a literal argument against a name that describes no type and reported
    # every correct call. ast.unparse is 3.9+; older interpreters keep the collapsed
    # form, which is no worse than what they had.
    if node is None:
        return ''
    try:
        return ast.unparse(node)
    except Exception:
        return expr_str(node)

def var_def(arg, optional=False, variadic=False, key_only=False):
    annot = annot_str(arg.annotation) if arg.annotation else ''
    return {'name': arg.arg, 'typing': annot, 'type_refs': annot_refs(arg.annotation),
            'optional': optional, 'variadic': variadic, 'key_only': key_only}

def def_lineno(node):
    # Where the declaration STARTS for a reader: node.lineno is the def/class line,
    # which sits below the decorators, so a read of a decorated declaration dropped
    # the @dataclass, @property or route that gives it its meaning.
    lines = [node.lineno]
    for d in getattr(node, 'decorator_list', []):
        ln = getattr(d, 'lineno', None)
        if ln is not None:
            lines.append(ln)
    return min(lines)

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

# Literal argument classes. A literal is reported as a CLASS rather than a type
# because that is what an assignability check needs; see contract.UntypedInt.
CONST_TOKENS = {'int': '#int', 'float': '#float', 'str': '#string',
                'bool': '#bool', 'NoneType': '#nil'}

def arg_token(node):
    # Only literals. Anything needing inference is left unknown on purpose: a wrong
    # token is a false warning about correct code, which is worse than no warning.
    if isinstance(node, ast.Constant):
        return CONST_TOKENS.get(type(node.value).__name__, '')
    return ''

def call_shape(node):
    # What the call passes, so a later scan can ask whether it still fits the callee.
    # A starred argument hides the real count, so the count is reported as unknown.
    starred = any(isinstance(a, ast.Starred) for a in node.args)
    kwargs_splat = any(k.arg is None for k in node.keywords)
    return {
        'argc': -1 if starred else len(node.args),
        'argtypes': [] if starred else [arg_token(a) for a in node.args],
        'starred': starred or kwargs_splat,
        'kw_names': [k.arg for k in node.keywords if k.arg is not None],
    }

def dotted_name(node):
    # 'a' or 'a.b.c' for a pure Name/Attribute chain; None when any link is a call,
    # subscript or other expression, whose value a dotted name would misdescribe.
    if isinstance(node, ast.Name):
        return node.id
    if isinstance(node, ast.Attribute):
        head = dotted_name(node.value)
        if head is not None:
            return head + '.' + node.attr
    return None

def extract_body_calls(body):
    calls = []
    for stmt in body:
        if isinstance(stmt, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            continue
        for node in walk_no_nested_scopes(stmt):
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute):
                val = node.func.value
                # A dotted receiver (pkg.sub.f(), mod.Cls.method()) is recorded whole, so
                # the resolver can bind it through a dotted import.
                obj = dotted_name(val)
                if obj is not None:
                    c = {
                        'object_name': obj,
                        'method_name': node.func.attr,
                        'func': obj + '.' + node.func.attr,
                        'lineno': node.lineno,
                    }
                    c.update(call_shape(node))
                    calls.append(c)
                elif (isinstance(val, ast.Call) and isinstance(val.func, ast.Name)
                      and val.func.id == 'super'):
                    # super().method(...): the 'super' object_name is a marker the
                    # Go resolver resolves against the receiver class's bases.
                    c = {
                        'object_name': 'super',
                        'method_name': node.func.attr,
                        'func': 'super().' + node.func.attr,
                        'lineno': node.lineno,
                    }
                    c.update(call_shape(node))
                    calls.append(c)
            elif isinstance(node, ast.Call) and isinstance(node.func, ast.Name):
                c = {
                    'object_name': '',
                    'method_name': '',
                    'func': node.func.id,
                    'lineno': node.lineno,
                }
                c.update(call_shape(node))
                calls.append(c)
            elif isinstance(node, ast.BinOp) and isinstance(node.left, ast.Name):
                # a + b -> a.__add__(b): record the operator as a call so an
                # overloaded dunder on a typed operand resolves to a Calls edge.
                dunder = BINOP_DUNDERS.get(type(node.op).__name__)
                if dunder:
                    # The shape is REAL, not absent: an operator passes exactly one
                    # argument, its right operand. Leaving it out recorded zero, and
                    # the matcher then reported every overload as missing its operand.
                    calls.append({
                        'object_name': node.left.id,
                        'method_name': dunder,
                        'func': node.left.id + '.' + dunder,
                        'lineno': getattr(node, 'lineno', 0),
                        'argc': 1,
                        'argtypes': [arg_token(node.right)],
                        'starred': False,
                        'kw_names': [],
                    })
            elif (isinstance(node, ast.Subscript) and isinstance(node.value, ast.Name)
                  and isinstance(getattr(node, 'ctx', None), ast.Load)):
                # a[i] (read) -> a.__getitem__(i).
                calls.append({
                    'object_name': node.value.id,
                    'method_name': '__getitem__',
                    'func': node.value.id + '.__getitem__',
                    'lineno': getattr(node, 'lineno', 0),
                    'argc': 1,
                    'argtypes': [arg_token(getattr(node, 'slice', None))],
                    'starred': False,
                    'kw_names': [],
                })
            elif type(node).__name__ == 'MatchClass':
                # A 'case ClassName(...)' pattern references the class as a use;
                # record the class name (as a bare reference) so the resolver
                # can emit a uses_class edge. 'pattern' marks it as no call at all:
                # matching a class pattern never runs the class's __init__.
                calls.append({
                    'object_name': '',
                    'method_name': '',
                    'func': expr_str(node.cls),
                    'lineno': getattr(node, 'lineno', 0),
                    'pattern': True,
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
        # A local bound inside a block is still a local of this function: descend
        # through control flow (not into nested def/class scopes), in source order.
        if t in ('If', 'Try', 'TryStar', 'For', 'AsyncFor', 'While', 'With', 'AsyncWith'):
            for nb in (getattr(stmt, 'body', []), getattr(stmt, 'orelse', []),
                       getattr(stmt, 'finalbody', [])):
                assignments.extend(extract_local_assignments(nb))
            for handler in getattr(stmt, 'handlers', []):
                assignments.extend(extract_local_assignments(getattr(handler, 'body', [])))
        elif t == 'Match':
            for case in getattr(stmt, 'cases', []):
                assignments.extend(extract_local_assignments(case.body))
    return assignments

def rebinding_wrapper(name, value):
    # For 'name = w(name, ...)' or 'name = w(...)(name)' -- a decorator applied by
    # hand -- the decorator expression, spelled as decorator_names spells '@w'.
    if isinstance(value, ast.Call) and any(
            isinstance(a, ast.Name) and a.id == name for a in value.args):
        return expr_str(value.func)
    return ''

def fold_rebindings(functions, classes, variables):
    # A module-level name bound by 'def' or 'class' and then re-assigned is one name,
    # and the definition is what a reader looks for: it has the source, the signature
    # and the callers' edges. So the variable is dropped rather than replacing it (the
    # two share an ID). A rebinding that wraps the function after its def is exactly a
    # decorator application ('handler = deco(handler)' is what '@deco' means), so it is
    # recorded as the function's outermost decorator.
    defined = {}
    for f in functions:
        defined.setdefault(f['name'], []).append(f)
    class_names = {c['name'] for c in classes}
    kept = []
    for v in variables:
        fs = defined.get(v['name'])
        if fs is None and v['name'] not in class_names:
            kept.append(v)
            continue
        for f in fs or []:
            if v.get('wrapper') and v['lineno'] > f['lineno']:
                f['decorators'].insert(0, v['wrapper'])
    return kept

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
                        'wrapper': rebinding_wrapper(target.id, item.value),
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

def parse_func(node, parent, import_map):
    decs = decorator_names(node)
    is_prop = any(d in ('property', 'cached_property', 'functools.cached_property')
                  or d.endswith('.setter') or d.endswith('.getter') for d in decs)
    params = []
    # defaults covers the LAST N of posonlyargs+args, and kw_defaults aligns
    # positionally with kwonlyargs using None for no default. Recording which
    # parameters may be omitted is what makes an argument-count check possible at all.
    a_args = getattr(node, 'args')
    positional = list(getattr(a_args, 'posonlyargs', [])) + list(getattr(a_args, 'args', []))
    defaults = list(getattr(a_args, 'defaults', []))
    first_defaulted = len(positional) - len(defaults)
    for i, a in enumerate(positional):
        if hasattr(a, 'arg'):
            params.append(var_def(a, optional=(i >= first_defaulted)))
    kw_defaults = list(getattr(a_args, 'kw_defaults', []))
    for i, a in enumerate(getattr(a_args, 'kwonlyargs', [])):
        if hasattr(a, 'arg'):
            has_def = i < len(kw_defaults) and kw_defaults[i] is not None
            params.append(var_def(a, optional=has_def, key_only=True))
    vararg = getattr(node.args, 'vararg', None)
    if vararg is not None:
        params.append(var_def(vararg, optional=True, variadic=True))
    kwarg = getattr(node.args, 'kwarg', None)
    if kwarg is not None:
        params.append(var_def(kwarg, optional=True, variadic=True, key_only=True))
    results = []
    if getattr(node, 'returns', None):
        results.append({'name': '', 'typing': annot_str(node.returns),
                        'type_refs': annot_refs(node.returns)})
    return {
        'name': node.name,
        'docstring': ast.get_docstring(node) or '',
        'decorators': decs,
        'is_async': type(node).__name__ == 'AsyncFunctionDef',
        'is_property': is_prop,
        'is_abstract': is_abstract(decs),
        'params': params,
        'results': results,
        'lineno': def_lineno(node),
        'end_lineno': getattr(node, 'end_lineno', node.lineno),
        'parent': parent,
        'body_calls': extract_body_calls(node.body),
        'body_assignments': extract_local_assignments(node.body),
    }

# Modules named by 'from m import *', which bind names no import_map key shows.
STAR_MODULES = []

PROTOCOL_NAMES = ('typing.Protocol', 'typing_extensions.Protocol')
ABC_NAMES = ('abc.ABC',)
ABCMETA_NAMES = ('abc.ABCMeta',)

def names_one_of(expr, import_map, qualified):
    # Whether a base or metaclass expression is one of the qualified names, judged by
    # what it is bound to rather than how it is spelled: 'Protocol' imported from
    # typing is a Protocol, and 'ProtocolError' or a project class named Protocol is not.
    head, sep, rest = expr.partition('.')
    if head in import_map:
        return (import_map[head] + sep + rest) in qualified
    if not sep:
        return any((m + '.' + expr) in qualified for m in STAR_MODULES)
    return False

def parse_class(node, import_map):
    bases = [expr_str(b) for b in node.bases]
    decs = decorator_names(node)

    # metaclass=Meta is a class keyword, not a base; record it so the resolver
    # can emit a uses_class edge to the metaclass.
    metaclass = ''
    for kw in getattr(node, 'keywords', []):
        if kw.arg == 'metaclass':
            metaclass = expr_str(kw.value)

    is_abc = (any(names_one_of(b, import_map, ABC_NAMES) for b in bases)
              or (metaclass != '' and names_one_of(metaclass, import_map, ABCMETA_NAMES)))
    is_protocol = any(names_one_of(b, import_map, PROTOCOL_NAMES) for b in bases)

    funcs, subclasses, vars_ = extract_body(node.body, node.name, import_map)
    has_abstract = any(f.get('is_abstract') for f in funcs)

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
        'lineno': def_lineno(node),
        'end_lineno': getattr(node, 'end_lineno', node.lineno),
    }

def parse_file(path, module_root):
    # Bytes, not text: ast.parse then decodes the way Python does, honouring a UTF-8
    # BOM and a PEP 263 coding cookie, both of which a str parse rejects.
    with open(path, 'rb') as f:
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
                if alias.name == '*' and mod and not level:
                    STAR_MODULES.append(mod)

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
    vars_ = fold_rebindings(funcs, classes, vars_)

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
