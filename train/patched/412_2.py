

__version__ = "4.0.0"
__author__  = 'Ivan Herman'
__contact__ = 'Ivan Herman, ivan@w3.org'
__license__ = 'W3C® SOFTWARE NOTICE AND LICENSE, http://www.w3.org/Consortium/Legal/2002/copyright-software-20021231'

name = "pyRdfa3"

import sys
PY3 = (sys.version_info[0] >= 3)

if PY3 :
	from io import StringIO
else :
	from StringIO import StringIO

import os
import xml.dom.minidom
if PY3 :
	from urllib.parse import urlparse
else :
	from urlparse import urlparse

import rdflib
from rdflib	import URIRef
from rdflib	import Literal
from rdflib	import BNode
from rdflib	import Namespace
if rdflib.__version__ >= "3.0.0" :
	from rdflib	import RDF  as ns_rdf
	from rdflib	import RDFS as ns_rdfs
	from rdflib	import Graph
else :
	from rdflib.RDFS  import RDFSNS as ns_rdfs
	from rdflib.RDF	  import RDFNS  as ns_rdf
	from rdflib.Graph import Graph

ns_rdfa		= Namespace("http://www.w3.org/ns/rdfa#")

from .extras.httpheader   import acceptable_content_type, content_type
from .transform.prototype import handle_prototypes

RDFA_VOCAB  = ns_rdfa["usesVocabulary"]

ns_xsd		= Namespace('http://www.w3.org/2001/XMLSchema#')

ns_distill	= Namespace("http://www.w3.org/2007/08/pyRdfa/vocab#")

debug = False

class RDFaError(Exception) :

	def __init__(self, msg) :
		self.msg = msg
		Exception.__init__(self)

class FailedSource(RDFaError) :

	def __init__(self, msg, http_code = None) :
		self.msg		= msg
		self.http_code 	= http_code
		RDFaError.__init__(self, msg)

class HTTPError(RDFaError) :

	def __init__(self, http_msg, http_code) :
		self.msg		= http_msg
		self.http_code	= http_code
		RDFaError.__init__(self,http_msg)

class ProcessingError(RDFaError) :

	pass

class pyRdfaError(Exception) :

	pass

RDFA_Error                  = ns_rdfa["Error"]
RDFA_Warning                = ns_rdfa["Warning"]
RDFA_Info                   = ns_rdfa["Information"]
NonConformantMarkup         = ns_rdfa["DocumentError"]
UnresolvablePrefix          = ns_rdfa["UnresolvedCURIE"]
UnresolvableReference       = ns_rdfa["UnresolvedCURIE"]
UnresolvableTerm            = ns_rdfa["UnresolvedTerm"]
VocabReferenceError         = ns_rdfa["VocabReferenceError"]
PrefixRedefinitionWarning   = ns_rdfa["PrefixRedefinition"]

FileReferenceError          = ns_distill["FileReferenceError"]
HTError                     = ns_distill["HTTPError"]
IncorrectPrefixDefinition   = ns_distill["IncorrectPrefixDefinition"]
IncorrectBlankNodeUsage     = ns_distill["IncorrectBlankNodeUsage"]
IncorrectLiteral            = ns_distill["IncorrectLiteral"]

err_no_blank_node                    = "Blank node in %s position is not allowed; ignored"

err_redefining_URI_as_prefix        = "'%s' a registered or an otherwise used URI scheme, but is defined as a prefix here; is this a mistake? (see, eg, http://en.wikipedia.org/wiki/URI_scheme or http://www.iana.org/assignments/uri-schemes.html for further information for most of the URI schemes)"
err_xmlns_deprecated                = "The usage of 'xmlns' for prefix definition is deprecated; please use the 'prefix' attribute instead (definition for '%s')"
err_bnode_local_prefix              = "The '_' local CURIE prefix is reserved for blank nodes, and cannot be defined as a prefix"
err_col_local_prefix                = "The character ':' is not valid in a CURIE Prefix, and cannot be used in a prefix definition (definition for '%s')"
err_missing_URI_prefix              = "Missing URI in prefix declaration for '%s' (in '%s')"
err_invalid_prefix                  = "Invalid prefix declaration '%s' (in '%s')"
err_no_default_prefix               = "Default prefix cannot be changed (in '%s')"
err_prefix_and_xmlns                = "@prefix setting for '%s' overrides the 'xmlns:%s' setting; may be a source of problem if same file is run through RDFa 1.0"
err_non_ncname_prefix               = "Non NCNAME '%s' in prefix definition (in '%s'); ignored"
err_absolute_reference              = "CURIE Reference part contains an authority part: %s (in '%s'); ignored"
err_query_reference                 = "CURIE Reference query part contains an unauthorized character: %s (in '%s'); ignored"
err_fragment_reference              = "CURIE Reference fragment part contains an unauthorized character: %s (in '%s'); ignored"
err_lang                            = "There is a problem with language setting; either both xml:lang and lang used on an element with different values, or, for (X)HTML5, only xml:lang is used."
err_URI_scheme                      = "Unusual URI scheme used in <%s>; may that be a mistake, e.g., resulting from using an undefined CURIE prefix or an incorrect CURIE?"
err_illegal_safe_CURIE              = "Illegal safe CURIE: %s; ignored"
err_no_CURIE_in_safe_CURIE          = "Safe CURIE is used, but the value does not correspond to a defined CURIE: [%s]; ignored"
err_undefined_terms                 = "'%s' is used as a term, but has not been defined as such; ignored"
err_non_legal_CURIE_ref             = "Relative URI is not allowed in this position (or not a legal CURIE reference) '%s'; ignored"
err_undefined_CURIE                 = "Undefined CURIE: '%s'; ignored"
err_prefix_redefinition             = "Prefix '%s' (defined in the initial RDFa context or in an ancestor) is redefined"

err_unusual_char_in_URI             = "Unusual character in uri: %s; possible error?"

from .state            import ExecutionContext
from .parse            import parse_one_node
from .options          import Options
from .transform        import top_about, empty_safe_curie, vocab_for_role
from .utils            import URIOpener
from .host             import HostLanguage, MediaTypes, preferred_suffixes, content_to_host_language

CACHE_DIR_VAR           = "PyRdfaCacheDir"

rdfa_current_version    = "1.1"

registered_iana_schemes = [
	"aaa","aaas","acap","cap","cid","crid","data","dav","dict","did","dns","fax","file", "ftp","geo","go",
	"gopher","h323","http","https","iax","icap","im","imap","info","ipp","iris","ldap", "lsid",
	"mailto","mid","modem","msrp","msrps", "mtqp", "mupdate","news","nfs","nntp","opaquelocktoken",
	"pop","pres", "prospero","rstp","rsync", "service","shttp","sieve","sip","sips", "sms", "snmp", "soap", "tag",
	"tel","telnet", "tftp", "thismessage","tn3270","tip","tv","urn","vemmi","wais","ws", "wss", "xmpp"
]

unofficial_common = [
	"about", "adiumxtra", "aim", "apt", "afp", "aw", "bitcoin", "bolo", "callto", "chrome", "coap",
	"content", "cvs", "doi", "ed2k", "facetime", "feed", "finger", "fish", "git", "gg",
	"gizmoproject", "gtalk", "irc", "ircs", "irc6", "itms", "jar", "javascript",
	"keyparc", "lastfm", "ldaps", "magnet", "maps", "market", "message", "mms",
	"msnim", "mumble", "mvn", "notes", "palm", "paparazzi", "psync", "rmi",
	"secondlife", "sgn", "skype", "spotify", "ssh", "sftp", "smb", "soldat",
	"steam", "svn", "teamspeak", "things", "udb", "unreal", "ut2004",
	"ventrillo", "view-source", "webcal", "wtai", "wyciwyg", "xfire", "xri", "ymsgr"
]

historical_iana_schemes = [
	"fax", "mailserver", "modem", "pack", "prospero", "snews", "videotex", "wais"
]

provisional_iana_schemes = [
	"afs", "dtn", "dvb", "icon", "ipn", "jms", "oid", "rsync", "ni"
]

other_used_schemes = [
	"hdl", "isbn", "issn", "mstp", "rtmp", "rtspu", "stp"
]

uri_schemes = registered_iana_schemes + unofficial_common + historical_iana_schemes + provisional_iana_schemes + other_used_schemes

builtInTransformers = [
	empty_safe_curie, top_about, vocab_for_role
]

class pyRdfa :

	def __init__(self, options = None, base = "", media_type = "", rdfa_version = None) :

		self.http_status = 200

		self.base = base
		if base == "" :
			self.required_base = None
		else :
			self.required_base	= base
		self.charset 		= None

		self.media_type = media_type

		if options == None :
			self.options = Options()
		else :
			self.options = options

		if media_type != "" :
			self.options.set_host_language(self.media_type)

		if rdfa_version is not None :
			self.rdfa_version = rdfa_version
		else :
			self.rdfa_version = None

	def _get_input(self, name) :

		try :
			isstring = isinstance(name, basestring)
		except :
			isstring = isinstance(name, str)

		try :
			if isstring :
				if urlparse(name)[0] != "" :
					url_request 	  = URIOpener(name)
					self.base 		  = url_request.location
					if self.media_type == "" :
						if url_request.content_type in content_to_host_language :
							self.media_type = url_request.content_type
						else :
							self.media_type = MediaTypes.xml
						self.options.set_host_language(self.media_type)
					self.charset = url_request.charset
					if self.required_base == None :
						self.required_base = name
					return url_request.data
				else :
					if self.required_base == None :
						self.required_base = "file://" + os.path.join(os.getcwd(),name)
					if self.media_type == "" :
						self.media_type = MediaTypes.xml
						for suffix in preferred_suffixes :
							if name.endswith(suffix) :
								self.media_type = preferred_suffixes[suffix]
								self.charset = 'utf-8'
								break
						self.options.set_host_language(self.media_type)
					return open(name, 'rb')
			else :
				return name
		except HTTPError :
			raise sys.exc_info()[1]
		except RDFaError as e :
			raise e
		except :
			(type, value, traceback) = sys.exc_info()
			raise FailedSource(value)

	@staticmethod
	def _validate_output_format(outputFormat):

		if outputFormat not in ["turtle", "n3", "xml", "pretty-xml", "nt", "json-ld"] :
			outputFormat = "turtle"
		return outputFormat

	def graph_from_DOM(self, dom, graph = None, pgraph = None) :

		def copyGraph(tog, fromg) :
			for t in fromg :
				tog.add(t)
			for k,ns in fromg.namespaces() :
				tog.bind(k,ns)

		if graph == None :
			graph   = Graph()

		default_graph = Graph()

		topElement = dom.documentElement

		state = ExecutionContext(topElement, default_graph, base=self.required_base if self.required_base != None else "", options=self.options, rdfa_version=self.rdfa_version)

		for trans in self.options.transformers + builtInTransformers :
			trans(topElement, self.options, state)

		self.rdfa_version = state.rdfa_version

		parse_one_node(topElement, default_graph, None, state, [])

		handle_prototypes(default_graph)

		if self.options.vocab_expansion :
			from .rdfs.process import process_rdfa_sem
			process_rdfa_sem(default_graph, self.options)

		if self.options.experimental_features :
			pass

		if self.options.output_default_graph :
			copyGraph(graph, default_graph)
			if self.options.output_processor_graph :
				if pgraph != None :
					copyGraph(pgraph, self.options.processor_graph.graph)
				else :
					copyGraph(graph, self.options.processor_graph.graph)
		elif self.options.output_processor_graph :
			if pgraph != None :
				copyGraph(pgraph, self.options.processor_graph.graph)
			else :
				copyGraph(graph, self.options.processor_graph.graph)
