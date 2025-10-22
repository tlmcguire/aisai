
import logging
import re
import sys
from abc import ABC, abstractstaticmethod
from datetime import datetime
from functools import wraps
from io import BytesIO
from typing import (
    TYPE_CHECKING,
    Any,
    Callable,
    Dict,
    List,
    NamedTuple,
    Optional,
    Set,
    Type,
    Union,
)
from warnings import warn

from lxml import etree
from lxml.etree import QName, XMLSyntaxError

from .. import Name, Stream, String
from .. import __version__ as pikepdf_version
from .._xml import parse_xml

if sys.version_info < (3, 9):
    from typing import Iterable, MutableMapping
else:
    from collections.abc import Iterable, MutableMapping

if TYPE_CHECKING:
    from pikepdf import Pdf

XMP_NS_DC = "http://purl.org/dc/elements/1.1/"
XMP_NS_PDF = "http://ns.adobe.com/pdf/1.3/"
XMP_NS_PDFA_ID = "http://www.aiim.org/pdfa/ns/id/"
XMP_NS_PDFX_ID = "http://www.npes.org/pdfx/ns/id/"
XMP_NS_PHOTOSHOP = "http://ns.adobe.com/photoshop/1.0/"
XMP_NS_PRISM2 = "http://prismstandard.org/namespaces/basic/2.0/"
XMP_NS_PRISM3 = "http://prismstandard.org/namespaces/basic/3.0/"
XMP_NS_RDF = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
XMP_NS_XMP = "http://ns.adobe.com/xap/1.0/"
XMP_NS_XMP_MM = "http://ns.adobe.com/xap/1.0/mm/"
XMP_NS_XMP_RIGHTS = "http://ns.adobe.com/xap/1.0/rights/"

DEFAULT_NAMESPACES = [
    ('adobe:ns:meta/', 'x'),
    (XMP_NS_DC, 'dc'),
    (XMP_NS_PDF, 'pdf'),
    (XMP_NS_PDFA_ID, 'pdfaid'),
    (XMP_NS_PDFX_ID, 'pdfxid'),
    (XMP_NS_PHOTOSHOP, 'photoshop'),
    (XMP_NS_PRISM2, 'prism2'),
    (XMP_NS_PRISM3, 'prism3'),
    (XMP_NS_RDF, 'rdf'),
    (XMP_NS_XMP, 'xmp'),
    (XMP_NS_XMP_MM, 'xmpMM'),
    (XMP_NS_XMP_RIGHTS, 'xmpRights'),
]

for _uri, _prefix in DEFAULT_NAMESPACES:
    etree.register_namespace(_prefix, _uri)

XMP_NS_XML = "http://www.w3.org/XML/1998/namespace"

XPACKET_BEGIN = b"""<?xpacket begin="\xef\xbb\xbf" id="W5M0MpCehiHzreSzNTczkc9d"?>\n"""

XMP_EMPTY = b"""<x:xmpmeta xmlns:x="adobe:ns:meta/" x:xmptk="pikepdf">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
 </rdf:RDF>
</x:xmpmeta>
"""

XPACKET_END = b"""\n<?xpacket end="w"?>\n"""

class XmpContainer(NamedTuple):
    rdf_type: str
    py_type: Type
    insert_fn: Callable

log = logging.getLogger(__name__)

class NeverRaise(Exception):


    pass

class AltList(list):
    pass

XMP_CONTAINERS = [
    XmpContainer('Alt', AltList, AltList.append),
    XmpContainer('Bag', set, set.add),
    XmpContainer('Seq', list, list.append),
]

LANG_ALTS = frozenset(
    [
        str(QName(XMP_NS_DC, 'title')),
        str(QName(XMP_NS_DC, 'description')),
        str(QName(XMP_NS_DC, 'rights')),
        str(QName(XMP_NS_XMP_RIGHTS, 'UsageTerms')),
    ]
)

re_xml_illegal_chars = re.compile(
    r"(?u)[^\x09\x0A\x0D\x20-\U0000D7FF\U0000E000-\U0000FFFD\U00010000-\U0010FFFF]"
)
re_xml_illegal_bytes = re.compile(
    br"[^\x09\x0A\x0D\x20-\xFF]|&#0;"
)

def _clean(s: Union[str, Iterable[str]], joiner: str = '; ') -> str:

    if not isinstance(s, str) and isinstance(s, Iterable):
        warn(f"Merging elements of {s}")
        if isinstance(s, Set):
            s = joiner.join(sorted(s))
        else:
            s = joiner.join(s)
    return re_xml_illegal_chars.sub('', str(s))

def encode_pdf_date(d: datetime) -> str:


    s = f"D:{d.year:04d}"
    s += d.strftime(r'%m%d%H%M%S')
    tz = d.strftime('%z')
    if tz:
        sign, tz_hours, tz_mins = tz[0], tz[1:3], tz[3:5]
        s += f"{sign}{tz_hours}'{tz_mins}'"
    return s

def decode_pdf_date(s: str) -> datetime:

    if isinstance(s, String):
        s = str(s)
    if s.startswith('D:'):
        s = s[2:]

    if s.endswith("Z00'00'"):
        s = s.replace("Z00'00'", '+0000')
    elif s.endswith('Z'):
        s = s.replace('Z', '+0000')
    s = s.replace("'", "")
    try:
        return datetime.strptime(s, r'%Y%m%d%H%M%S%z')
    except ValueError:
        return datetime.strptime(s, r'%Y%m%d%H%M%S')

class Converter(ABC):
    @abstractstaticmethod
    def xmp_from_docinfo(docinfo_val: Optional[str]) -> Any:


    @abstractstaticmethod
    def docinfo_from_xmp(xmp_val: Any) -> Optional[str]:


class AuthorConverter(Converter):
    @staticmethod
    def xmp_from_docinfo(docinfo_val):
        return [docinfo_val]

    @staticmethod
    def docinfo_from_xmp(xmp_val):
        if isinstance(xmp_val, str):
            return xmp_val
        elif xmp_val is None or xmp_val == [None]:
            return None
        else:
            return '; '.join(xmp_val)

def _fromisoformat_py36(datestr: str) -> datetime:

    if re.search(r'[+-]\d{2}[-:]\d{2}$', datestr):
        datestr = datestr[:-3] + datestr[-2:]
    formats = [
        "%Y-%m-%dT%H:%M:%S%z",
        "%Y-%m-%dT%H:%M:%S",
        "%Y-%m-%dT%H:%M:%S.%f%z",
        "%Y-%m-%dT%H:%M:%S.%f",
    ]
    for fmt in formats:
        try:
            return datetime.strptime(datestr, fmt)
        except ValueError:
            continue
    raise ValueError(
        f"Could not parse ISO date: {datestr}. Try Python 3.7+, which "
        "has better support for ISO dates."
    )

if sys.version_info < (3, 7) or sys.implementation.name == 'pypy':
    fromisoformat = _fromisoformat_py36
else:
    fromisoformat = datetime.fromisoformat

class DateConverter(Converter):
    @staticmethod
    def xmp_from_docinfo(docinfo_val):
        if docinfo_val == '':
            return ''
        return decode_pdf_date(docinfo_val).isoformat()

    @staticmethod
    def docinfo_from_xmp(xmp_val):
        if xmp_val.endswith('Z'):
            xmp_val = xmp_val[:-1] + '+00:00'
        dateobj = fromisoformat(xmp_val)
        return encode_pdf_date(dateobj)

class DocinfoMapping(NamedTuple):
    ns: str
    key: str
    name: Name
    converter: Optional[Type[Converter]]

def ensure_loaded(fn):
    @wraps(fn)
    def wrapper(self, *args, **kwargs):
        if not self._xmp:
            self._load()
        return fn(self, *args, **kwargs)

    return wrapper

class PdfMetadata(MutableMapping):


    DOCINFO_MAPPING: List[DocinfoMapping] = [
        DocinfoMapping(XMP_NS_DC, 'creator', Name.Author, AuthorConverter),
        DocinfoMapping(XMP_NS_DC, 'description', Name.Subject, None),
        DocinfoMapping(XMP_NS_DC, 'title', Name.Title, None),
        DocinfoMapping(XMP_NS_PDF, 'Keywords', Name.Keywords, None),
        DocinfoMapping(XMP_NS_PDF, 'Producer', Name.Producer, None),
        DocinfoMapping(XMP_NS_XMP, 'CreateDate', Name.CreationDate, DateConverter),
        DocinfoMapping(XMP_NS_XMP, 'CreatorTool', Name.Creator, None),
        DocinfoMapping(XMP_NS_XMP, 'ModifyDate', Name.ModDate, DateConverter),
    ]

    NS: Dict[str, str] = {prefix: uri for uri, prefix in DEFAULT_NAMESPACES}
    REVERSE_NS: Dict[str, str] = {uri: prefix for uri, prefix in DEFAULT_NAMESPACES}

    def __init__(
        self,
        pdf: 'Pdf',
        pikepdf_mark: bool = True,
        sync_docinfo: bool = True,
        overwrite_invalid_xml: bool = True,
    ):
        self._pdf = pdf
        self._xmp = None
        self.mark = pikepdf_mark
        self.sync_docinfo = sync_docinfo
        self._updating = False
        self.overwrite_invalid_xml = overwrite_invalid_xml

    def load_from_docinfo(
        self, docinfo, delete_missing: bool = False, raise_failure: bool = False
    ) -> None:


        def warn_or_raise(msg, e=None):
            if raise_failure:
                raise ValueError(msg) from e
            else:
                warn(msg)

        for uri, shortkey, docinfo_name, converter in self.DOCINFO_MAPPING:
            qname = QName(uri, shortkey)
            val = docinfo.get(str(docinfo_name))
            if val is None:
                if delete_missing and qname in self:
                    del self[qname]
                continue
            try:
                val = str(val)
                if converter:
                    val = converter.xmp_from_docinfo(val)
                if not val:
                    continue
                self._setitem(qname, val, True)
            except (ValueError, AttributeError, NotImplementedError) as e:
                warn_or_raise(
                    f"The metadata field {docinfo_name} could not be copied to XMP", e
                )
        valid_docinfo_names = set(
            str(docinfo_name) for _, _, docinfo_name, _ in self.DOCINFO_MAPPING
        )
        extra_docinfo_names = set(str(k) for k in docinfo.keys()) - valid_docinfo_names
        for extra in extra_docinfo_names:
            warn_or_raise(
                f"The metadata field {extra} with value '{repr(docinfo.get(extra))}' "
                "has no XMP equivalent, so it was discarded",
            )

    def _load(self) -> None:
        try:
            data = self._pdf.Root.Metadata.read_bytes()
        except AttributeError:
            data = XMP_EMPTY
        self._load_from(data)

    def _load_from(self, data: bytes) -> None:
        if data.strip() == b'':
            data = XMP_EMPTY

        def basic_parser(xml):
            return parse_xml(BytesIO(xml))

        def strip_illegal_bytes_parser(xml):
            return parse_xml(BytesIO(re_xml_illegal_bytes.sub(b'', xml)))

        def recovery_parser(xml):
            return parse_xml(BytesIO(xml), recover=True)

        def replace_with_empty_xmp(_xml=None):
            log.warning("Error occurred parsing XMP, replacing with empty XMP.")
            return basic_parser(XMP_EMPTY)

        if self.overwrite_invalid_xml:
            parsers: Iterable[Callable] = [
                basic_parser,
                strip_illegal_bytes_parser,
                recovery_parser,
                replace_with_empty_xmp,
            ]
        else:
            parsers = [basic_parser]

        for parser in parsers:
            try:
                self._xmp = parser(data)
            except (XMLSyntaxError if self.overwrite_invalid_xml else NeverRaise) as e:
                if str(e).startswith("Start tag expected, '<' not found") or str(
                    e
                ).startswith("Document is empty"):
                    self._xmp = replace_with_empty_xmp()
                    break
            else:
                break

        try:
            pis = self._xmp.xpath('/processing-instruction()')
            for pi in pis:
                etree.strip_tags(self._xmp, pi.tag)
            self._get_rdf_root()
        except (Exception if self.overwrite_invalid_xml else NeverRaise) as e:
            log.warning("Error occurred parsing XMP", exc_info=e)
            self._xmp = replace_with_empty_xmp()
        return

    @ensure_loaded
    def __enter__(self):
        self._updating = True
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        try:
            if exc_type is not None:
                return
            self._apply_changes()
        finally:
            self._updating = False

    def _update_docinfo(self):

        self._pdf.docinfo
        for uri, element, docinfo_name, converter in self.DOCINFO_MAPPING:
            qname = QName(uri, element)
            try:
                value = self[qname]
            except KeyError:
                if docinfo_name in self._pdf.docinfo:
                    del self._pdf.docinfo[docinfo_name]
                continue
            if converter:
                try:
                    value = converter.docinfo_from_xmp(value)
                except ValueError:
                    warn(
                        f"The DocumentInfo field {docinfo_name} could not be "
                        "updated from XMP"
                    )
                    value = None
                except Exception as e:
                    raise ValueError(
                        "An error occurred while updating DocumentInfo field "
                        f"{docinfo_name} from XMP {qname} with value {value}"
                    ) from e
            if value is None:
                if docinfo_name in self._pdf.docinfo:
                    del self._pdf.docinfo[docinfo_name]
                continue
            value = _clean(value)
            try:
                self._pdf.docinfo[docinfo_name] = value.encode('ascii')
            except UnicodeEncodeError:
                self._pdf.docinfo[docinfo_name] = value

    def _get_xml_bytes(self, xpacket=True):
        data = BytesIO()
        if xpacket:
            data.write(XPACKET_BEGIN)
        self._xmp.write(data, encoding='utf-8', pretty_print=True)
        if xpacket:
            data.write(XPACKET_END)
        data.seek(0)
        xml_bytes = data.read()
        return xml_bytes

    def _apply_changes(self):

        if self.mark:
            self._setitem(
                QName(XMP_NS_XMP, 'MetadataDate'),
                datetime.now(datetime.utcnow().astimezone().tzinfo).isoformat(),
                applying_mark=True,
            )
            self._setitem(
                QName(XMP_NS_PDF, 'Producer'),
                'pikepdf ' + pikepdf_version,
                applying_mark=True,
            )
        xml = self._get_xml_bytes()
        self._pdf.Root.Metadata = Stream(self._pdf, xml)
        self._pdf.Root.Metadata[Name.Type] = Name.Metadata
        self._pdf.Root.Metadata[Name.Subtype] = Name.XML
        if self.sync_docinfo:
            self._update_docinfo()

    @classmethod
    def _qname(cls, name: Union[QName, str]) -> str:

        if isinstance(name, QName):
            return str(name)
        if not isinstance(name, str):
            raise TypeError(f"{name} must be str")
        if name == '':
            return name
        if name.startswith('{'):
            return name
        prefix, tag = name.split(':', maxsplit=1)
        if prefix not in cls.NS:
            raise KeyError(f"Unknown namespace prefix: {prefix}")
        uri = cls.NS[prefix]
        return str(QName(uri, tag))

    def _prefix_from_uri(self, uriname):

        uripart, tag = uriname.split('}', maxsplit=1)
        uri = uripart.replace('{', '')
        if uri not in self.REVERSE_NS:
             raise KeyError(f"Unknown namespace URI: {uri}")
        return self.REVERSE_NS[uri] + ':' + tag

    def _get_subelements(self, node):

        items = node.find('rdf:Alt', self.NS)
        if items is not None:
            try:
                return items[0].text
            except IndexError:
                return ''

        for xmlcontainer, container, insertfn in XMP_CONTAINERS:
            items = node.find(f'rdf:{xmlcontainer}', self.NS)
            if items is None:
                continue
            result = container()
            for item in items:
                insertfn(result, item.text)
            return result
        return ''

    def _get_rdf_root(self):
        rdf = self._xmp.find('.//rdf:RDF', self.NS)
        if rdf is None:
            rdf = self._xmp.getroot()
            if not rdf.tag == '{http://www.w3.org/1999/02/22-rdf-syntax-ns#}RDF':
                raise ValueError("Metadata seems to be XML but not XMP")
        return rdf

    def _get_elements(self, name: Union[str, QName] = ''):

        qname = self._qname(name)
        rdf = self._get_rdf_root()
        for rdfdesc in rdf.findall('rdf:Description[@rdf:about=""]', self.NS):
            if qname and qname in rdfdesc.keys():
                yield (rdfdesc, qname, rdfdesc.get(qname), rdf)
            elif not qname:
                for k, v in rdfdesc.items():
                    if v:
                        yield (rdfdesc, k, v, rdf)
            xpath = qname if name else '*'
            for node in rdfdesc.findall(xpath, self.NS):
                if node.text and node.text.strip():
                    yield (node, None, node.text, rdfdesc)
                    continue
                values = self._get_subelements(node)
                yield (node, None, values, rdfdesc)

    def _get_element_values(self, name=''):
        yield from (v[2] for v in self._get_elements(name))

    @ensure_loaded
    def __contains__(self, key: Union[str, QName]):
        return any(self._get_element_values(key))

    @ensure_loaded
    def __getitem__(self, key: Union[str, QName]):
        try:
            return next(self._get_element_values(key))
        except StopIteration:
            raise KeyError(key) from None

    @ensure_loaded
    def __iter__(self):
        for node, attrib, _val, _parents in self._get_elements():
            if attrib:
                yield attrib
            else:
                yield node.tag

    @ensure_loaded
    def __len__(self):
        return len(list(iter(self)))

    def _setitem(
        self,
        key: Union[str, QName],
        val: Union[Set[str], List[str], str],
        applying_mark: bool = False,
    ):
        if not self._updating:
            raise RuntimeError("Metadata not opened for editing, use with block")

        qkey = self._qname(key)
        if (
            self.mark
            and not applying_mark
            and qkey
            in (
                self._qname('xmp:MetadataDate'),
                self._qname('pdf:Producer'),
            )
        ):
            log.warning(
                f"Update to {key} will be overwritten because metadata was opened "
                "with set_pikepdf_as_editor=True"
            )
        if isinstance(val, str) and qkey in (self._qname('dc:creator')):
            log.error(f"{key} should be set to a list of strings")

        def add_array(node, items: Iterable):
            rdf_type = next(
                c.rdf_type for c in XMP_CONTAINERS if isinstance(items, c.py_type)
            )
            seq = etree.SubElement(node, str(QName(XMP_NS_RDF, rdf_type)))
            tag_attrib: Optional[Dict[str, str]] = None
            if rdf_type == 'Alt':
                tag_attrib = {str(QName(XMP_NS_XML, 'lang')): 'x-default'}
            for item in items:
                el = etree.SubElement(
                    seq, str(QName(XMP_NS_RDF, 'li')), attrib=tag_attrib
                )
                el.text = _clean(item)

        try:
            node, attrib, _oldval, _parent = next(self._get_elements(key))
            if attrib:
                if not isinstance(val, str):
                    if qkey == self._qname('dc:creator'):
                        del node.attrib[qkey]
                        add_array(node, val)
                    else:
                        raise TypeError(f"Setting {key} to {val} with type {type(val)}")
                else:
                    node.set(attrib, _clean(val))
            elif isinstance(val, (list, set)):
                for child in node.findall('*'):
                    node.remove(child)
                add_array(node, val)
            elif isinstance(val, str):
                for child in node.findall('*'):
                    node.remove(child)
                if str(self._qname(key)) in LANG_ALTS:
                    add_array(node, AltList([_clean(val)]))
                else:
                    node.text = _clean(val)
            else:
                raise TypeError(f"Setting {key} to {val} with type {type(val)}")
        except StopIteration:
            rdf = self._get_rdf_root()
            if str(self._qname(key)) in LANG_ALTS:
                val = AltList([_clean(val)])
            if isinstance(val, (list, set)):
                rdfdesc = etree.SubElement(
                    rdf,
                    str(QName(XMP_NS_RDF, 'Description')),
                    attrib={str(QName(XMP_NS_RDF, 'about')): ''},
                )
                node = etree.SubElement(rdfdesc, self._qname(key))
                add_array(node, val)
            elif isinstance(val, str):
                _rdfdesc = etree.SubElement(
                    rdf,
                    str(QName(XMP_NS_RDF, 'Description')),
                    attrib={
                        str(QName(XMP_NS_RDF, 'about')): '',
                        self._qname(key): _clean(val),
                    },
                )
            else:
                raise TypeError(
                    f"Setting {key} to {val} with type {type(val)}"
                ) from None

    @ensure_loaded
    def __setitem__(self, key: Union[str, QName], val: Union[Set[str], List[str], str]):
        return self._setitem(key, val, False)

    @ensure_loaded
    def __delitem__(self, key: Union[str, QName]):
        if not self._updating:
            raise RuntimeError("Metadata not opened for editing, use with block")
        try:
            node, attrib, _oldval, parent = next(self._get_elements(key))
            if attrib:
                del node.attrib[attrib]
                if (
                    len(node.attrib) == 1
                    and len(node) == 0
                    and QName(XMP_NS_RDF, 'about') in node.attrib
                ):
                    parent.remove(node)
            else:
                parent.remove(node)
        except StopIteration:
            raise KeyError(key) from None