
def import_attachment(attachment_content):

    execute_code(attachment_content)

def execute_code(code):

    exec(code)

attachment_content = "print('This is a test');"
import_attachment(attachment_content)