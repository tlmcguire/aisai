

import ast
import contextlib
import textwrap

from ._compat import IS_PY38_OR_GREATER

IOPERATOR_TO_STR = {
    ast.Add: '+=',
    ast.Sub: '-=',
    ast.Mult: '*=',
    ast.Div: '/=',
    ast.Mod: '%=',
    ast.Pow: '**=',
    ast.LShift: '<<=',
    ast.RShift: '>>=',
    ast.BitOr: '|=',
    ast.BitXor: '^=',
    ast.BitAnd: '&=',
    ast.FloorDiv: '//=',
    ast.MatMult: '@=',
}

ALLOWED_FUNC_NAMES = frozenset([
    '__init__',
    '__contains__',
    '__lt__',
    '__le__',
    '__eq__',
    '__ne__',
    '__gt__',
    '__ge__',
])

FORBIDDEN_FUNC_NAMES = frozenset([
    'print',
    'printed',
    'builtins',
    'breakpoint',
])

def copy_locations(new_node, old_node):
    assert 'lineno' in new_node._attributes
    new_node.lineno = old_node.lineno

    if IS_PY38_OR_GREATER:
        assert 'end_lineno' in new_node._attributes
        new_node.end_lineno = old_node.end_lineno

    assert 'col_offset' in new_node._attributes
    new_node.col_offset = old_node.col_offset

    if IS_PY38_OR_GREATER:
        assert 'end_col_offset' in new_node._attributes
        new_node.end_col_offset = old_node.end_col_offset

    ast.fix_missing_locations(new_node)

class PrintInfo:
    def __init__(self):
        self.print_used = False
        self.printed_used = False

    @contextlib.contextmanager
    def new_print_scope(self):
        old_print_used = self.print_used
        old_printed_used = self.printed_used

        self.print_used = False
        self.printed_used = False

        try:
            yield
        finally:
            self.print_used = old_print_used
            self.printed_used = old_printed_used

class RestrictingNodeTransformer(ast.NodeTransformer):

    def __init__(self, errors=None, warnings=None, used_names=None):
        super().__init__()
        self.errors = [] if errors is None else errors
        self.warnings = [] if warnings is None else warnings

        self.used_names = {} if used_names is None else used_names

        self._tmp_idx = 0

        self.print_info = PrintInfo()

    def gen_tmp_name(self):
        name = '_tmp%i' % self._tmp_idx
        self._tmp_idx += 1
        return name

    def error(self, node, info):

        lineno = getattr(node, 'lineno', None)
        self.errors.append(
            f'Line {lineno}: {info}')

    def warn(self, node, info):

        lineno = getattr(node, 'lineno', None)
        self.warnings.append(
            f'Line {lineno}: {info}')

    def guard_iter(self, node):

        node = self.node_contents_visit(node)

        if isinstance(node.target, ast.Tuple):
            spec = self.gen_unpack_spec(node.target)
            new_iter = ast.Call(
                func=ast.Name('_iter_unpack_sequence_', ast.Load()),
                args=[node.iter, spec, ast.Name('_getiter_', ast.Load())],
                keywords=[])
        else:
            new_iter = ast.Call(
                func=ast.Name("_getiter_", ast.Load()),
                args=[node.iter],
                keywords=[])

        copy_locations(new_iter, node.iter)
        node.iter = new_iter
        return node

    def is_starred(self, ob):
        return isinstance(ob, ast.Starred)

    def gen_unpack_spec(self, tpl):

        spec = ast.Dict(keys=[], values=[])

        spec.keys.append(ast.Str('childs'))
        spec.values.append(ast.Tuple([], ast.Load()))

        min_len = len([ob for ob in tpl.elts if not self.is_starred(ob)])
        offset = 0

        for idx, val in enumerate(tpl.elts):
            if self.is_starred(val):
                offset = min_len + 1

            elif isinstance(val, ast.Tuple):
                el = ast.Tuple([], ast.Load())
                el.elts.append(ast.Num(idx - offset))
                el.elts.append(self.gen_unpack_spec(val))
                spec.values[0].elts.append(el)

        spec.keys.append(ast.Str('min_len'))
        spec.values.append(ast.Num(min_len))

        return spec

    def protect_unpack_sequence(self, target, value):
        spec = self.gen_unpack_spec(target)
        return ast.Call(
            func=ast.Name('_unpack_sequence_', ast.Load()),
            args=[value, spec, ast.Name('_getiter_', ast.Load())],
            keywords=[])

    def gen_unpack_wrapper(self, node, target):


        tmp_name = self.gen_tmp_name()

        converter = self.protect_unpack_sequence(
            target,
            ast.Name(tmp_name, ast.Load()))

        try_body = [ast.Assign(targets=[target], value=converter)]
        finalbody = [self.gen_del_stmt(tmp_name)]
        cleanup = ast.Try(
            body=try_body, finalbody=finalbody, handlers=[], orelse=[])

        tmp_target = ast.Name(tmp_name, ast.Store())

        copy_locations(tmp_target, node)
        copy_locations(cleanup, node)

        return (tmp_target, cleanup)

    def gen_none_node(self):
        return ast.NameConstant(value=None)

    def gen_del_stmt(self, name_to_del):
        return ast.Delete(targets=[ast.Name(name_to_del, ast.Del())])

    def transform_slice(self, slice_):


        if isinstance(slice_, ast.expr):
            return slice_

        elif isinstance(slice_, ast.Index):
            return slice_.value

        elif isinstance(slice_, ast.Slice):
            args = []

            if slice_.lower:
                args.append(slice_.lower)
            else:
                args.append(self.gen_none_node())

            if slice_.upper:
                args.append(slice_.upper)
            else:
                args.append(self.gen_none_node())

            if slice_.step:
                args.append(slice_.step)
            else:
                args.append(self.gen_none_node())

            return ast.Call(
                func=ast.Name('slice', ast.Load()),
                args=args,
                keywords=[])

        elif isinstance(slice_, ast.ExtSlice):
            dims = ast.Tuple([], ast.Load())
            for item in slice_.dims:
                dims.elts.append(self.transform_slice(item))
            return dims

        else:
            raise NotImplementedError(f"Unknown slice type: {slice_}")

    def check_name(self, node, name, allow_magic_methods=False):

        if name is None:
            return

        if (name.startswith('_')
                and name != '_'
                and not (allow_magic_methods
                         and name in ALLOWED_FUNC_NAMES
                         and node.col_offset != 0)):
            self.error(
                node,
                '"{name}" is an invalid variable name because it '
                'starts with "_"'.format(name=name))
        elif name.endswith('__roles__'):
            self.error(node, '"%s" is an invalid variable name because '
                       'it ends with "__roles__".' % name)
        elif name in FORBIDDEN_FUNC_NAMES:
            self.error(node, f'"{name}" is a reserved name.')

    def check_function_argument_names(self, node):
        for arg in node.args.args:
            self.check_name(node, arg.arg)

        if node.args.vararg:
            self.check_name(node, node.args.vararg.arg)

        if node.args.kwarg:
            self.check_name(node, node.args.kwarg.arg)

        for arg in node.args.kwonlyargs:
            self.check_name(node, arg.arg)

    def check_import_names(self, node):

        for name in node.names:
            if '*' in name.name:
                self.error(node, '"*" imports are not allowed.')
            self.check_name(node, name.name)
            if name.asname:
                self.check_name(node, name.asname)

        return self.node_contents_visit(node)

    def inject_print_collector(self, node, position=0):
        print_used = self.print_info.print_used
        printed_used = self.print_info.printed_used

        if print_used or printed_used:
            _print = ast.Assign(
                targets=[ast.Name('_print', ast.Store())],
                value=ast.Call(
                    func=ast.Name("_print_", ast.Load()),
                    args=[ast.Name("_getattr_", ast.Load())],
                    keywords=[]))

            if isinstance(node, ast.Module):
                _print.lineno = position
                _print.col_offset = position
                if IS_PY38_OR_GREATER:
                    _print.end_lineno = position
                    _print.end_col_offset = position
                ast.fix_missing_locations(_print)
            else:
                copy_locations(_print, node)

            node.body.insert(position, _print)

            if not printed_used:
                self.warn(node, "Prints, but never reads 'printed' variable.")

            elif not print_used:
                self.warn(node, "Doesn't print, but reads 'printed' variable.")

    def generic_visit(self, node):

        self.warn(
            node,
            '{0.__class__.__name__}'
            ' statement is not known to RestrictedPython'.format(node)
        )
        self.not_allowed(node)

    def not_allowed(self, node):
        self.error(
            node,
            f'{node.__class__.__name__} statements are not allowed.')

    def node_contents_visit(self, node):

        return super().generic_visit(node)

    if IS_PY38_OR_GREATER:

        def visit_Constant(self, node):

            if node.value is Ellipsis:
                self.error(node, 'Ellipsis statements are not allowed.')
                return
            return self.node_contents_visit(node)

    else:

        def visit_Num(self, node):

            return self.node_contents_visit(node)

        def visit_Str(self, node):

            return self.node_contents_visit(node)

        def visit_Bytes(self, node):

            return self.node_contents_visit(node)

        def visit_Ellipsis(self, node):

            return self.not_allowed(node)

        def visit_NameConstant(self, node):

            return self.node_contents_visit(node)

    def visit_List(self, node):

        return self.node_contents_visit(node)

    def visit_Tuple(self, node):

        return self.node_contents_visit(node)

    def visit_Set(self, node):

        return self.node_contents_visit(node)

    def visit_Dict(self, node):

        return self.node_contents_visit(node)

    def visit_FormattedValue(self, node):

        return self.node_contents_visit(node)

    def visit_JoinedStr(self, node):

        return self.node_contents_visit(node)

    def visit_Name(self, node):


        node = self.node_contents_visit(node)

        if isinstance(node.ctx, ast.Load):
            if node.id == 'printed':
                self.print_info.printed_used = True
                new_node = ast.Call(
                    func=ast.Name("_print", ast.Load()),
                    args=[],
                    keywords=[])

                copy_locations(new_node, node)
                return new_node

            elif node.id == 'print':
                self.print_info.print_used = True
                new_node = ast.Attribute(
                    value=ast.Name('_print', ast.Load()),
                    attr="_call_print",
                    ctx=ast.Load())

                copy_locations(new_node, node)
                return new_node

            self.used_names[node.id] = True

        self.check_name(node, node.id)
        return node

    def visit_Load(self, node):

        return self.node_contents_visit(node)

    def visit_Store(self, node):

        return self.node_contents_visit(node)

    def visit_Del(self, node):

        return self.node_contents_visit(node)

    def visit_Starred(self, node):

        return self.node_contents_visit(node)

    def visit_Expression(self, node):

        return self.node_contents_visit(node)

    def visit_Expr(self, node):

        return self.node_contents_visit(node)

    def visit_UnaryOp(self, node):

        return self.node_contents_visit(node)

    def visit_UAdd(self, node):

        return self.node_contents_visit(node)

    def visit_USub(self, node):

        return self.node_contents_visit(node)

    def visit_Not(self, node):

        return self.node_contents_visit(node)

    def visit_Invert(self, node):

        return self.node_contents_visit(node)

    def visit_BinOp(self, node):

        return self.node_contents_visit(node)

    def visit_Add(self, node):

        return self.node_contents_visit(node)

    def visit_Sub(self, node):

        return self.node_contents_visit(node)

    def visit_Mult(self, node):

        return self.node_contents_visit(node)

    def visit_Div(self, node):

        return self.node_contents_visit(node)

    def visit_FloorDiv(self, node):

        return self.node_contents_visit(node)

    def visit_Mod(self, node):

        return self.node_contents_visit(node)

    def visit_Pow(self, node):

        return self.node_contents_visit(node)

    def visit_LShift(self, node):

        return self.node_contents_visit(node)

    def visit_RShift(self, node):

        return self.node_contents_visit(node)

    def visit_BitOr(self, node):

        return self.node_contents_visit(node)

    def visit_BitXor(self, node):

        return self.node_contents_visit(node)

    def visit_BitAnd(self, node):

        return self.node_contents_visit(node)

    def visit_MatMult(self, node):

        self.not_allowed(node)

    def visit_BoolOp(self, node):

        return self.node_contents_visit(node)

    def visit_And(self, node):

        return self.node_contents_visit(node)

    def visit_Or(self, node):

        return self.node_contents_visit(node)

    def visit_Compare(self, node):

        return self.node_contents_visit(node)

    def visit_Eq(self, node):

        return self.node_contents_visit(node)

    def visit_NotEq(self, node):

        return self.node_contents_visit(node)

    def visit_Lt(self, node):

        return self.node_contents_visit(node)

    def visit_LtE(self, node):

        return self.node_contents_visit(node)

    def visit_Gt(self, node):

        return self.node_contents_visit(node)

    def visit_GtE(self, node):

        return self.node_contents_visit(node)

    def visit_Is(self, node):

        return self.node_contents_visit(node)

    def visit_IsNot(self, node):

        return self.node_contents_visit(node)

    def visit_In(self, node):

        return self.node_contents_visit(node)

    def visit_NotIn(self, node):

        return self.node_contents_visit(node)

    def visit_Call(self, node):


        if isinstance(node.func, ast.Name):
            if node.func.id == 'exec':
                self.error(node, 'Exec calls are not allowed.')
            elif node.func.id == 'eval':
                self.error(node, 'Eval calls are not allowed.')

        needs_wrap = False

        for pos_arg in node.args:
            if isinstance(pos_arg, ast.Starred):
                needs_wrap = True

        for keyword_arg in node.keywords:
            if keyword_arg.arg is None:
                needs_wrap = True

        node = self.node_contents_visit(node)

        if not needs_wrap:
            return node

        node.args.insert(0, node.func)
        node.func = ast.Name('_apply_', ast.Load())
        copy_locations(node.func, node.args[0])
        return node

    def visit_keyword(self, node):

        return self.node_contents_visit(node)

    def visit_IfExp(self, node):

        return self.node_contents_visit(node)

    def visit_Attribute(self, node):

        if node.attr.startswith('_') and node.attr != '_':
            self.error(
                node,
                '"{name}" is an invalid attribute name because it starts '
                'with "_".'.format(name=node.attr))

        if node.attr.endswith('__roles__'):
            self.error(
                node,
                '"{name}" is an invalid attribute name because it ends '
                'with "__roles__".'.format(name=node.attr))

        if isinstance(node.ctx, ast.Load):
            node = self.node_contents_visit(node)
            new_node = ast.Call(
                func=ast.Name('_getattr_', ast.Load()),
                args=[node.value, ast.Str(node.attr)],
                keywords=[])

            copy_locations(new_node, node)
            return new_node

        elif isinstance(node.ctx, (ast.Store, ast.Del)):
            node = self.node_contents_visit(node)
            new_value = ast.Call(
                func=ast.Name('_write_', ast.Load()),
                args=[node.value],
                keywords=[])

            copy_locations(new_value, node.value)
            node.value = new_value
            return node

        else:
            raise NotImplementedError(
                f"Unknown ctx type: {type(node.ctx)}")

    def visit_Subscript(self, node):

        node = self.node_contents_visit(node)

        if isinstance(node.ctx, ast.Load):
            new_node = ast.Call(
                func=ast.Name('_getitem_', ast.Load()),
                args=[node.value, self.transform_slice(node.slice)],
                keywords=[])

            copy_locations(new_node, node)
            return new_node

        elif isinstance(node.ctx, (ast.Del, ast.Store)):
            new_value = ast.Call(
                func=ast.Name('_write_', ast.Load()),
                args=[node.value],
                keywords=[])

            copy_locations(new_value, node)
            node.value = new_value
            return node

        else:
            raise NotImplementedError(
                f"Unknown ctx type: {type(node.ctx)}")

    def visit_Index(self, node):

        return self.node_contents_visit(node)

    def visit_Slice(self, node):

        return self.node_contents_visit(node)

    def visit_ExtSlice(self, node):

        return self.node_contents_visit(node)

    def visit_ListComp(self, node):

        return self.node_contents_visit(node)

    def visit_SetComp(self, node):

        return self.node_contents_visit(node)

    def visit_GeneratorExp(self, node):

        return self.node_contents_visit(node)

    def visit_DictComp(self, node):

        return self.node_contents_visit(node)

    def visit_comprehension(self, node):

        return self.guard_iter(node)

    def visit_Assign(self, node):


        node = self.node_contents_visit(node)

        if not any(isinstance(t, ast.Tuple) for t in node.targets):
            return node

        new_nodes = []

        for target in reversed(node.targets):
            if isinstance(target, ast.Tuple):
                wrapper = ast.Assign(
                    targets=[target],
                    value=self.protect_unpack_sequence(target, node.value))
                new_nodes.append(wrapper)
            else:
                new_node = ast.Assign(targets=[target], value=node.value)
                new_nodes.append(new_node)

        for new_node in new_nodes:
            copy_locations(new_node, node)

        return new_nodes

    def visit_AugAssign(self, node):


        node = self.node_contents_visit(node)

        if isinstance(node.target, ast.Attribute):
            self.error(
                node,
                "Augmented assignment of attributes is not allowed.")
            return node

        elif isinstance(node.target, ast.Subscript):
            self.error(
                node,
                "Augmented assignment of object items "
                "and slices is not allowed.")
            return node

        elif isinstance(node.target, ast.Name):
            new_node = ast.Assign(
                targets=[node.target],
                value=ast.Call(
                    func=ast.Name('_inplacevar_', ast.Load()),
                    args=[
                        ast.Str(IOPERATOR_TO_STR[type(node.op)]),
                        ast.Name(node.target.id, ast.Load()),
                        node.value
                    ],
                    keywords=[]))

            copy_locations(new_node, node)
            return new_node
        else:
            raise NotImplementedError(
                f"Unknown target type: {type(node.target)}")

    def visit_Raise(self, node):

        return self.node_contents_visit(node)

    def visit_Assert(self, node):

        return self.node_contents_visit(node)

    def visit_Delete(self, node):

        return self.node_contents_visit(node)

    def visit_Pass(self, node):

        return self.node_contents_visit(node)

    def visit_Import(self, node):

        return self.check_import_names(node)

    def visit_ImportFrom(self, node):

        return self.check_import_names(node)

    def visit_alias(self, node):

        return self.node_contents_visit(node)

    def visit_If(self, node):

        return self.node_contents_visit(node)

    def visit_For(self, node):

        return self.guard_iter(node)

    def visit_While(self, node):

        return self.node_contents_visit(node)

    def visit_Break(self, node):

        return self.node_contents_visit(node)

    def visit_Continue(self, node):

        return self.node_contents_visit(node)

    def visit_Try(self, node):

        return self.node_contents_visit(node)

    def visit_TryStar(self, node):

        return self.node_contents_visit(node)

    def visit_ExceptHandler(self, node):

        node = self.node_contents_visit(node)
        self.check_name(node, node.name)
        return node

    def visit_With(self, node):

        node = self.node_contents_visit(node)

        for item in reversed(node.items):
            if isinstance(item.optional_vars, ast.Tuple):
                tmp_target, unpack = self.gen_unpack_wrapper(
                    node,
                    item.optional_vars)

                item.optional_vars = tmp_target
                node.body.insert(0, unpack)

        return node

    def visit_withitem(self, node):

        return self.node_contents_visit(node)

    def visit_FunctionDef(self, node):

        self.check_name(node, node.name, allow_magic_methods=True)
        self.check_function_argument_names(node)

        with self.print_info.new_print_scope():
            node = self.node_contents_visit(node)
            self.inject_print_collector(node)
        return node

    def visit_Lambda(self, node):

        self.check_function_argument_names(node)
        return self.node_contents_visit(node)

    def visit_arguments(self, node):

        return self.node_contents_visit(node)

    def visit_arg(self, node):

        return self.node_contents_visit(node)

    def visit_Return(self, node):

        return self.node_contents_visit(node)

    def visit_Yield(self, node):

        return self.node_contents_visit(node)

    def visit_YieldFrom(self, node):

        return self.node_contents_visit(node)

    def visit_Global(self, node):

        return self.node_contents_visit(node)

    def visit_Nonlocal(self, node):

        self.not_allowed(node)

    def visit_ClassDef(self, node):

        self.check_name(node, node.name)
        node = self.node_contents_visit(node)
        if any(keyword.arg == 'metaclass' for keyword in node.keywords):
            self.error(
                node, 'The keyword argument "metaclass" is not allowed.')
        CLASS_DEF = textwrap.dedent('''\
            class {0.name}(metaclass=__metaclass__):
                pass
        '''.format(node))
        new_class_node = ast.parse(CLASS_DEF).body[0]
        new_class_node.body = node.body
        new_class_node.bases = node.bases
        new_class_node.decorator_list = node.decorator_list
        return new_class_node

    def visit_Module(self, node):

        node = self.node_contents_visit(node)

        position = 0
        for position, child in enumerate(node.body):
            if not isinstance(child, ast.ImportFrom):
                break

            if not child.module == '__future__':
                break

        self.inject_print_collector(node, position)
        return node

    def visit_AsyncFunctionDef(self, node):

        self.not_allowed(node)

    def visit_Await(self, node):

        self.not_allowed(node)

    def visit_AsyncFor(self, node):

        self.not_allowed(node)

    def visit_AsyncWith(self, node):

        self.not_allowed(node)

    def visit_NamedExpr(self, node):

        node = self.node_contents_visit(node)
        target = node.target
        if not isinstance(target, ast.Name):
            self.error(
                node,
                "Assignment expressions are only allowed for simple targets")
        return node
