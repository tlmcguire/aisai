

from __future__ import annotations

import ast
from typing import Any, Dict, List, Optional

from langchain.chains.base import Chain
from langchain.chains.llm import LLMChain
from langchain_community.utilities import PythonREPL
from langchain_core.callbacks.manager import CallbackManagerForChainRun
from langchain_core.language_models import BaseLanguageModel

from langchain_experimental.pal_chain.colored_object_prompt import COLORED_OBJECT_PROMPT
from langchain_experimental.pal_chain.math_prompt import MATH_PROMPT
from langchain_experimental.pydantic_v1 import Extra, Field, root_validator

COMMAND_EXECUTION_FUNCTIONS = ["system", "exec", "execfile", "eval", "__import__"]
COMMAND_EXECUTION_ATTRIBUTES = [
    "__import__",
    "__subclasses__",
    "__builtins__",
    "__globals__",
    "__getattribute__",
    "__bases__",
    "__mro__",
    "__base__",
]

class PALValidation:


    SOLUTION_EXPRESSION_TYPE_FUNCTION = ast.FunctionDef
    SOLUTION_EXPRESSION_TYPE_VARIABLE = ast.Name

    def __init__(
        self,
        solution_expression_name: Optional[str] = None,
        solution_expression_type: Optional[type] = None,
        allow_imports: bool = False,
        allow_command_exec: bool = False,
    ):

        self.solution_expression_name = solution_expression_name
        self.solution_expression_type = solution_expression_type

        if solution_expression_name is not None:
            if not isinstance(self.solution_expression_name, str):
                raise ValueError(
                    f"Expected solution_expression_name to be str, "
                    f"instead found {type(self.solution_expression_name)}"
                )
        if solution_expression_type is not None:
            if (
                self.solution_expression_type
                is not self.SOLUTION_EXPRESSION_TYPE_FUNCTION
                and self.solution_expression_type
                is not self.SOLUTION_EXPRESSION_TYPE_VARIABLE
            ):
                raise ValueError(
                    f"Expected solution_expression_type to be one of "
                    f"({self.SOLUTION_EXPRESSION_TYPE_FUNCTION},"
                    f"{self.SOLUTION_EXPRESSION_TYPE_VARIABLE}),"
                    f"instead found {self.solution_expression_type}"
                )

        if solution_expression_name is not None and solution_expression_type is None:
            raise TypeError(
                "solution_expression_name "
                "requires solution_expression_type to be passed as well"
            )
        if solution_expression_name is None and solution_expression_type is not None:
            raise TypeError(
                "solution_expression_type "
                "requires solution_expression_name to be passed as well"
            )

        self.allow_imports = allow_imports
        self.allow_command_exec = allow_command_exec

class PALChain(Chain):


    llm_chain: LLMChain
    stop: str = "\n\n"

    get_answer_expr: str = "print(solution())"

    python_globals: Optional[Dict[str, Any]] = None

    python_locals: Optional[Dict[str, Any]] = None

    output_key: str = "result"
    return_intermediate_steps: bool = False

    code_validations: PALValidation = Field(default_factory=PALValidation)

    timeout: Optional[int] = 10

    allow_dangerous_code: bool = False

    @root_validator(pre=False, skip_on_failure=True)
    def post_init(cls, values: Dict) -> Dict:
        if not values["allow_dangerous_code"]:
            raise ValueError(
                "This chain relies on the execution of generated code, "
                "which can be dangerous. "
                "Please read the security notice for this class, and only "
                "use it if you understand the security implications. "
                "If you want to proceed, you will need to opt-in, by setting "
                "`allow_dangerous_code` to `True`."
            )

        return values

    class Config:


        extra = Extra.forbid
        arbitrary_types_allowed = True

    @property
    def input_keys(self) -> List[str]:

        return self.llm_chain.prompt.input_variables

    @property
    def output_keys(self) -> List[str]:

        if not self.return_intermediate_steps:
            return [self.output_key]
        else:
            return [self.output_key, "intermediate_steps"]

    def _call(
        self,
        inputs: Dict[str, Any],
        run_manager: Optional[CallbackManagerForChainRun] = None,
    ) -> Dict[str, str]:
        _run_manager = run_manager or CallbackManagerForChainRun.get_noop_manager()
        code = self.llm_chain.predict(
            stop=[self.stop], callbacks=_run_manager.get_child(), **inputs
        )
        _run_manager.on_text(code, color="green", end="\n", verbose=self.verbose)
        PALChain.validate_code(code, self.code_validations)

        repl = PythonREPL(
            _globals=self.python_globals,
            _locals=self.python_locals,
        )
        res = repl.run(code + f"\n{self.get_answer_expr}", timeout=self.timeout)
        output = {self.output_key: res.strip()}
        if self.return_intermediate_steps:
            output["intermediate_steps"] = code
        return output

    @classmethod
    def validate_code(cls, code: str, code_validations: PALValidation) -> None:
        try:
            code_tree = ast.parse(code)
        except (SyntaxError, UnicodeDecodeError):
            raise ValueError(f"Generated code is not valid python code: {code}")
        except TypeError:
            raise ValueError(
                f"Generated code is expected to be a string, "
                f"instead found {type(code)}"
            )
        except OverflowError:
            raise ValueError(
                f"Generated code too long / complex to be parsed by ast: {code}"
            )

        found_solution_expr = False
        if code_validations.solution_expression_name is None:
            found_solution_expr = True

        has_imports = False
        top_level_nodes = list(ast.iter_child_nodes(code_tree))
        for node in top_level_nodes:
            if (
                code_validations.solution_expression_name is not None
                and code_validations.solution_expression_type is not None
            ):
                if (
                    isinstance(node, code_validations.solution_expression_type)
                    and hasattr(node, "name")
                    and node.name == code_validations.solution_expression_name
                ):
                    found_solution_expr = True
                if isinstance(node, ast.Assign):
                    for target_node in node.targets:
                        if (
                            isinstance(
                                target_node, code_validations.solution_expression_type
                            )
                            and hasattr(target_node, "id")
                            and target_node.id
                            == code_validations.solution_expression_name
                        ):
                            found_solution_expr = True
            if isinstance(node, ast.Import) or isinstance(node, ast.ImportFrom):
                has_imports = True

        if not found_solution_expr:
            raise ValueError(
                f"Generated code is missing the solution expression: "
                f"{code_validations.solution_expression_name} of type: "
                f"{code_validations.solution_expression_type}"
            )

        if not code_validations.allow_imports and has_imports:
            raise ValueError(f"Generated code has disallowed imports: {code}")

        if (
            not code_validations.allow_command_exec
            or not code_validations.allow_imports
        ):
            for node in ast.walk(code_tree):
                if (
                    not code_validations.allow_command_exec
                    and isinstance(node, ast.Attribute)
                    and node.attr in COMMAND_EXECUTION_ATTRIBUTES
                ):
                    raise ValueError(
                        f"Found illegal command execution function "
                        f"{node.attr} in code {code}"
                    )
                if (not code_validations.allow_command_exec) and isinstance(
                    node, ast.Call
                ):
                    if (
                        hasattr(node.func, "id")
                        and node.func.id in COMMAND_EXECUTION_FUNCTIONS
                    ):
                        raise ValueError(
                            f"Found illegal command execution function "
                            f"{node.func.id} in code {code}"
                        )

                    if (
                        isinstance(node.func, ast.Attribute)
                        and node.func.attr in COMMAND_EXECUTION_FUNCTIONS
                    ):
                        raise ValueError(
                            f"Found illegal command execution function "
                            f"{node.func.attr} in code {code}"
                        )

                if (not code_validations.allow_imports) and (
                    isinstance(node, ast.Import) or isinstance(node, ast.ImportFrom)
                ):
                    raise ValueError(f"Generated code has disallowed imports: {code}")

    @classmethod
    def from_math_prompt(cls, llm: BaseLanguageModel, **kwargs: Any) -> PALChain:

        llm_chain = LLMChain(llm=llm, prompt=MATH_PROMPT)
        code_validations = PALValidation(
            solution_expression_name="solution",
            solution_expression_type=PALValidation.SOLUTION_EXPRESSION_TYPE_FUNCTION,
        )

        return cls(
            llm_chain=llm_chain,
            stop="\n\n",
            get_answer_expr="print(solution())",
            code_validations=code_validations,
            **kwargs,
        )

    @classmethod
    def from_colored_object_prompt(
        cls, llm: BaseLanguageModel, **kwargs: Any
    ) -> PALChain:

        llm_chain = LLMChain(llm=llm, prompt=COLORED_OBJECT_PROMPT)
        code_validations = PALValidation(
            solution_expression_name="answer",
            solution_expression_type=PALValidation.SOLUTION_EXPRESSION_TYPE_VARIABLE,
        )
        return cls(
            llm_chain=llm_chain,
            stop="\n\n\n",
            get_answer_expr="print(answer)",
            code_validations=code_validations,
            **kwargs,
        )

    @property
    def _chain_type(self) -> str:
        return "pal_chain"
