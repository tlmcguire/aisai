
def safe_import_attachment(attachment_content):

    safe_content = escape_user_input(attachment_content)
    process_safe_content(safe_content)

def escape_user_input(user_input):

    escape_mapping = {
        '<': '&lt;',
        '>': '&gt;',
        '{': '&#123;',
        '}': '&#125;',
        '$': '&#36;',
        '#': '&#35;',
        '%': '&#37;',
        '&': '&amp;',
    }

    for char, escaped in escape_mapping.items():
        user_input = user_input.replace(char, escaped)

    return user_input

def process_safe_content(content):
    print("Processing safe content:", content)

attachment_content = "<script>alert('XSS');</script>"
safe_import_attachment(attachment_content)