def render_document_tree_macro(params):

    output = f"<div class='document-tree'>{params}</div>"
    return output

params = {
    'param1': 'value1',
    'param2': '{malicious_code}',
}

vulnerable_output = render_document_tree_macro(params)
print(vulnerable_output)