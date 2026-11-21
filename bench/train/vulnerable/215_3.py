import copy
import warnings

from bs4 import BeautifulSoup

from .utils import LinkNotFoundError

class InvalidFormMethod(LinkNotFoundError):

    pass

class Form:


    def __init__(self, form):
        if form.name != 'form':
            warnings.warn(
                f"Constructed a Form from a '{form.name}' instead of a 'form' "
                " element. This may be an error in a future version of "
                "MechanicalSoup.", FutureWarning)

        self.form = form
        self._submit_chosen = False

        self.attach = self.set_input
        self.input = self.set_input
        self.textarea = self.set_textarea

    def set_input(self, data):


        for (name, value) in data.items():
            i = self.form.find("input", {"name": name})
            if not i:
                raise InvalidFormMethod("No input field named " + name)
            i["value"] = value

    def uncheck_all(self, name):

        for option in self.form.find_all("input", {"name": name}):
            if "checked" in option.attrs:
                del option.attrs["checked"]

    def check(self, data):

        for (name, value) in data.items():
            try:
                self.set_checkbox({name: value}, uncheck_other_boxes=False)
                continue
            except InvalidFormMethod:
                pass
            try:
                self.set_radio({name: value})
                continue
            except InvalidFormMethod:
                pass
            raise LinkNotFoundError("No input checkbox/radio named " + name)

    def set_checkbox(self, data, uncheck_other_boxes=True):

        for (name, value) in data.items():
            selector = 'input[type="checkbox" i][name="{}"]'.format(name)
            checkboxes = self.form.select(selector)
            if not checkboxes:
                raise InvalidFormMethod("No input checkbox named " + name)

            if uncheck_other_boxes:
                self.uncheck_all(name)

            if not isinstance(value, list) and not isinstance(value, tuple):
                value = (value,)

            for choice in value:
                choice_str = str(choice)
                for checkbox in checkboxes:
                    if checkbox.attrs.get("value", "on") == choice_str:
                        checkbox["checked"] = ""
                        break
                    elif choice is True:
                        checkbox["checked"] = ""
                        break
                    elif choice is False:
                        if "checked" in checkbox.attrs:
                            del checkbox.attrs["checked"]
                        break
                else:
                    raise LinkNotFoundError(
                        "No input checkbox named %s with choice %s" %
                        (name, choice)
                    )

    def set_radio(self, data):

        for (name, value) in data.items():
            selector = 'input[type="radio" i][name="{}"]'.format(name)
            radios = self.form.select(selector)
            if not radios:
                raise InvalidFormMethod("No input radio named " + name)

            self.uncheck_all(name)

            for radio in radios:
                if radio.attrs.get("value", "on") == str(value):
                    radio["checked"] = ""
                    break
            else:
                raise LinkNotFoundError(
                    f"No input radio named {name} with choice {value}"
                )

    def set_textarea(self, data):

        for (name, value) in data.items():
            t = self.form.find("textarea", {"name": name})
            if not t:
                raise InvalidFormMethod("No textarea named " + name)
            t.string = value

    def set_select(self, data):

        for (name, value) in data.items():
            select = self.form.find("select", {"name": name})
            if not select:
                raise InvalidFormMethod("No select named " + name)

            for option in select.find_all("option"):
                if "selected" in option.attrs:
                    del option.attrs["selected"]

            if not isinstance(value, list) and not isinstance(value, tuple):
                value = (value,)
            elif "multiple" not in select.attrs:
                raise LinkNotFoundError("Cannot select multiple options!")

            for choice in value:
                option = select.find("option", {"value": choice})

                if not option:
                    option = select.find("option", string=choice)

                if not option:
                    raise LinkNotFoundError(
                        f'Option {choice} not found for select {name}'
                    )

                option.attrs["selected"] = "selected"

    def __setitem__(self, name, value):

        return self.set(name, value)

    def set(self, name, value, force=False):

        for func in ("checkbox", "radio", "input", "textarea", "select"):
            try:
                getattr(self, "set_" + func)({name: value})
                return
            except InvalidFormMethod:
                pass
        if force:
            self.new_control('text', name, value=value)
            return
        raise LinkNotFoundError("No valid element named " + name)

    def new_control(self, type, name, value, **kwargs):

        for tag in ('input', 'textarea', 'select'):
            for old in self.form.find_all(tag, {'name': name}):
                old.decompose()
        control = BeautifulSoup("", "html.parser").new_tag('input')
        control['type'] = type
        control['name'] = name
        control['value'] = value
        for k, v in kwargs.items():
            control[k] = v
        self.form.append(control)
        return control

    def choose_submit(self, submit):

        if self._submit_chosen:
            if submit is None:
                return
            else:
                raise Exception('Submit already chosen. Cannot change submit!')

        inps = [i for i in self.form.select('input[type="submit" i], button')
                if i.get("type", "").lower() not in ('button', 'reset')]

        if submit is None and inps:
            submit = inps[0]

        found = False
        for inp in inps:
            if (inp.has_attr('name') and inp['name'] == submit):
                if found:
                    raise LinkNotFoundError(
                        f"Multiple submit elements match: {submit}"
                    )
                found = True
            elif inp == submit:
                if found:
                    del inp['name']
                found = True
            else:
                del inp['name']

        if not found and submit is not None and submit is not False:
            raise LinkNotFoundError(
                f"Specified submit element not found: {submit}"
            )
        self._submit_chosen = True

    def print_summary(self):

        for input in self.form.find_all(
                ("input", "textarea", "select", "button")):
            input_copy = copy.copy(input)
            for subtag in input_copy.find_all() + [input_copy]:
                if subtag.string:
                    subtag.string = subtag.string.strip()
            print(input_copy)
