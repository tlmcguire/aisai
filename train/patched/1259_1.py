import ast
from asteval import Interpreter

class SafeInterpreter(Interpreter):
    def on_formattedvalue(self, node, fmt_str, val):

      if not isinstance(val, str):
          raise TypeError("Formatted value must be a string to avoid code injection.")

      if '{' in val or '}' in val:
        raise ValueError("Formatted value cannot contain braces to prevent format string injection")

      return str(fmt_str).format(__fstring__=val)