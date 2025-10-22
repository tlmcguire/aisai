import html

def escape_macro_parameters(params):

    safe_params = {}
    for key, value in params.items():
        safe_key = html.escape(str(key))
        safe_value = html.escape(str(value))
        safe_params[safe_key] = safe_value
    return safe_params

def render_document_tree_macro(params):

    safe_params = escape_macro_parameters(params)
    return safe_params

params = {
    'param1': 'value1',
    'param2': '{malicious_code}',
}

safe_output = render_document_tree_macro(params)
print(safe_output)