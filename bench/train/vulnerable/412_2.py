

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
					return open(name)
			else :
				return name
		except HTTPError :
			raise sys.exc_info()[1]
		except RDFaError as e :
			raise e
		except :
			(type, value, traceback) = sys.exc_info()
			raise FailedSource(value)

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

		self.options.reset_processor_graph()

		return graph

	def graph_from_source(self, name, graph = None, rdfOutput = False, pgraph = None) :

		def copyErrors(tog, options) :
			if tog == None :
				tog = Graph()
			if options.output_processor_graph :
				for t in options.processor_graph.graph :
					tog.add(t)
					if pgraph != None : pgraph.add(t)
				for k,ns in options.processor_graph.graph.namespaces() :
					tog.bind(k,ns)
					if pgraph != None : pgraph.bind(k,ns)
			options.reset_processor_graph()
			return tog

		try :
			isstring = isinstance(name, basestring)
		except :
			isstring = isinstance(name, str)

		try :
			input = None
			try :
				input = self._get_input(name)
			except FailedSource as ex :
				f = sys.exc_info()[1]
				self.http_status = 400
				if not rdfOutput : raise Exception(ex.msg)
				err = self.options.add_error(ex.msg, FileReferenceError, name)
				self.options.processor_graph.add_http_context(err, 400)
				return copyErrors(graph, self.options)
			except HTTPError as ex :
				h = sys.exc_info()[1]
				self.http_status = h.http_code
				if not rdfOutput : raise Exception(ex.msg)
				err = self.options.add_error("HTTP Error: %s (%s)" % (h.http_code,h.msg), HTError, name)
				self.options.processor_graph.add_http_context(err, h.http_code)
				return copyErrors(graph, self.options)
			except RDFaError as ex:
				e = sys.exc_info()[1]
				self.http_status = 500
				if not rdfOutput : raise Exception(ex.msg)
				err = self.options.add_error(str(ex.msg), context = name)
				self.options.processor_graph.add_http_context(err, 500)
				return copyErrors(graph, self.options)
			except Exception as ex :
				e = sys.exc_info()[1]
				self.http_status = 500
				if not rdfOutput : raise ex
				err = self.options.add_error(str(e), context = name)
				self.options.processor_graph.add_http_context(err, 500)
				return copyErrors(graph, self.options)

			dom = None
			try :
				msg = ""
				parser = None
				if self.options.host_language == HostLanguage.html5 :
					import warnings
					warnings.filterwarnings("ignore", category=DeprecationWarning)
					import html5lib
					parser = html5lib.HTMLParser(tree=html5lib.treebuilders.getTreeBuilder("dom"))
					if self.charset :
						dom = parser.parse(input)
					else :
						dom = parser.parse(input)

					try :
						if isstring :
							input.close()
							input = self._get_input(name)
						else :
							input.seek(0)
						from .host import adjust_html_version
						self.rdfa_version = adjust_html_version(input, self.rdfa_version)
					except :
						pass

				else :
					from .host import adjust_xhtml_and_version
					if isinstance(input, StringIO) or isinstance(input, file):
						parse = xml.dom.minidom.parse
					else:
						parse = xml.dom.minidom.parseString
					dom = parse(input)
					(adjusted_host_language, version) = adjust_xhtml_and_version(dom, self.options.host_language, self.rdfa_version)
					self.options.host_language = adjusted_host_language
					self.rdfa_version          = version
			except ImportError :
				msg = "HTML5 parser not available. Try installing html5lib <http://code.google.com/p/html5lib>"
				raise ImportError(msg)
			except Exception :
				e = sys.exc_info()[1]
				err = self.options.add_error(str(e), context = name)
				self.http_status = 400
				self.options.processor_graph.add_http_context(err, 400)
				return copyErrors(graph, self.options)

			return self.graph_from_DOM(dom, graph, pgraph)
		except Exception :
			(a,b,c) = sys.exc_info()
			sys.excepthook(a,b,c)
			if isinstance(b, ImportError) :
				self.http_status = None
			else :
				self.http_status = 500
			if not rdfOutput : raise b
			err = self.options.add_error(str(b), context = name)
			self.options.processor_graph.add_http_context(err, 500)
			return copyErrors(graph, self.options)

	def rdf_from_sources(self, names, outputFormat = "turtle", rdfOutput = False) :


		if rdflib.__version__ >= "3.0.0" :
			graph = Graph()
		else :
			try :
				from pyRdfaExtras import MyGraph
				graph = MyGraph()
			except :
				graph = Graph()

		for name in names :
			self.graph_from_source(name, graph, rdfOutput)

		if PY3 :
			return str(graph.serialize(format=outputFormat), encoding='utf-8')
		else :
			return graph.serialize(format=outputFormat)

	def rdf_from_source(self, name, outputFormat = "turtle", rdfOutput = False) :

		return self.rdf_from_sources([name], outputFormat, rdfOutput)

def processURI(uri, outputFormat, form={}) :

	def _get_option(param, compare_value, default) :
		param_old = param.replace('_','-')
		if param in list(form.keys()) :
			val = form.getfirst(param).lower()
			return val == compare_value
		elif param_old in list(form.keys()) :
			val = form.getfirst(param_old).lower()
			return val == compare_value
		else :
			return default

	if uri == "uploaded:" :
		input	= form["uploaded"].file
		base	= ""
	elif uri == "text:" :
		input	= StringIO(form.getfirst("text"))
		base	= ""
	else :
		input	= uri
		base	= uri

	if "rdfa_version" in list(form.keys()) :
		rdfa_version = form.getfirst("rdfa_version")
	else :
		rdfa_version = None

	if "host_language" in list(form.keys()) :
		if form.getfirst("host_language").lower() == "xhtml" :
			media_type = MediaTypes.xhtml
		elif form.getfirst("host_language").lower() == "html" :
			media_type = MediaTypes.html
		elif form.getfirst("host_language").lower() == "svg" :
			media_type = MediaTypes.svg
		elif form.getfirst("host_language").lower() == "atom" :
			media_type = MediaTypes.atom
		else :
			media_type = MediaTypes.xml
	else :
		media_type = ""

	transformers = []

	check_lite = "rdfa_lite" in list(form.keys()) and form.getfirst("rdfa_lite").lower() == "true"

	if "extras" in list(form.keys()) and form.getfirst("extras").lower() == "true" :
		from .transform.metaname              	import meta_transform
		from .transform.OpenID                	import OpenID_transform
		from .transform.DublinCore            	import DC_transform
		for t in [OpenID_transform, DC_transform, meta_transform] :
			transformers.append(t)
	else :
		if "extra-meta" in list(form.keys()) and form.getfirst("extra-meta").lower() == "true" :
			from .transform.metaname import meta_transform
			transformers.append(meta_transform)
		if "extra-openid" in list(form.keys()) and form.getfirst("extra-openid").lower() == "true" :
			from .transform.OpenID import OpenID_transform
			transformers.append(OpenID_transform)
		if "extra-dc" in list(form.keys()) and form.getfirst("extra-dc").lower() == "true" :
			from .transform.DublinCore import DC_transform
			transformers.append(DC_transform)

	output_default_graph 	= True
	output_processor_graph 	= False
	a = None
	if "graph" in list(form.keys()) :
		a = form.getfirst("graph").lower()
	elif "rdfagraph" in list(form.keys()) :
		a = form.getfirst("rdfagraph").lower()
	if a != None :
		if a == "processor" :
			output_default_graph 	= False
			output_processor_graph 	= True
		elif a == "processor,output" or a == "output,processor" :
			output_processor_graph 	= True

	embedded_rdf        = _get_option( "embedded_rdf", "true", False)
	space_preserve      = _get_option( "space_preserve", "true", True)
	vocab_cache         = _get_option( "vocab_cache", "true", True)
	vocab_cache_report  = _get_option( "vocab_cache_report", "true", False)
	refresh_vocab_cache = _get_option( "vocab_cache_refresh", "true", False)
	vocab_expansion     = _get_option( "vocab_expansion", "true", False)
	if vocab_cache_report : output_processor_graph = True

	options = Options(output_default_graph   = output_default_graph,
					  output_processor_graph = output_processor_graph,
					  space_preserve         = space_preserve,
					  transformers           = transformers,
					  vocab_cache            = vocab_cache,
					  vocab_cache_report     = vocab_cache_report,
					  refresh_vocab_cache    = refresh_vocab_cache,
					  vocab_expansion        = vocab_expansion,
					  embedded_rdf           = embedded_rdf,
					  check_lite             = check_lite
					  )
	processor = pyRdfa(options = options, base = base, media_type = media_type, rdfa_version = rdfa_version)

	htmlOutput = False

	try :
		graph = processor.rdf_from_source(input, outputFormat, rdfOutput = ("forceRDFOutput" in list(form.keys())) or not htmlOutput)
		if outputFormat == "n3" :
			retval = 'Content-Type: text/rdf+n3; charset=utf-8\n'
		elif outputFormat == "nt" or outputFormat == "turtle" :
			retval = 'Content-Type: text/turtle; charset=utf-8\n'
		elif outputFormat == "json-ld" or outputFormat == "json" :
			retval = 'Content-Type: application/ld+json; charset=utf-8\n'
		else :
			retval = 'Content-Type: application/rdf+xml; charset=utf-8\n'
		retval += '\n'
		retval += graph
		return retval
	except HTTPError :
		(type,h,traceback) = sys.exc_info()
		import cgi

		retval = 'Content-type: text/html; charset=utf-8\nStatus: %s \n\n' % h.http_code
		retval += "<html>\n"
		retval += "<head>\n"
		retval += "<title>HTTP Error in distilling RDFa content</title>\n"
		retval += "</head><body>\n"
		retval += "<h1>HTTP Error in distilling RDFa content</h1>\n"
		retval += "<p>HTTP Error: %s (%s)</p>\n" % (h.http_code,h.msg)
		retval += "<p>On URI: <code>'%s'</code></p>\n" % cgi.escape(uri)
		retval +="</body>\n"
		retval +="</html>\n"
		return retval
	except :
		(type,value,traceback) = sys.exc_info()

		import traceback, cgi

		retval = 'Content-type: text/html; charset=utf-8\nStatus: %s\n\n' % processor.http_status
		retval += "<html>\n"
		retval += "<head>\n"
		retval += "<title>Exception in RDFa processing</title>\n"
		retval += "</head><body>\n"
		retval += "<h1>Exception in distilling RDFa</h1>\n"
		retval += "<pre>\n"
		strio  = StringIO()
		traceback.print_exc(file=strio)
		retval += strio.getvalue()
		retval +="</pre>\n"
		retval +="<pre>%s</pre>\n" % value
		retval +="<h1>Distiller request details</h1>\n"
		retval +="<dl>\n"
		if uri == "text:" and "text" in form and form["text"].value != None and len(form["text"].value.strip()) != 0 :
			retval +="<dt>Text input:</dt><dd>%s</dd>\n" % cgi.escape(form["text"].value).replace('\n','<br/>')
		elif uri == "uploaded:" :
			retval +="<dt>Uploaded file</dt>\n"
		else :
			retval +="<dt>URI received:</dt><dd><code>'%s'</code></dd>\n" % cgi.escape(uri)
		if "host_language" in list(form.keys()) :
			retval +="<dt>Media Type:</dt><dd>%s</dd>\n" % media_type
		if "graph" in list(form.keys()) :
			retval +="<dt>Requested graphs:</dt><dd>%s</dd>\n" % form.getfirst("graph").lower()
		else :
			retval +="<dt>Requested graphs:</dt><dd>default</dd>\n"
		retval +="<dt>Output serialization format:</dt><dd> %s</dd>\n" % outputFormat
		if "space_preserve" in form : retval +="<dt>Space preserve:</dt><dd> %s</dd>\n" % form["space_preserve"].value
		retval +="</dl>\n"
		retval +="</body>\n"
		retval +="</html>\n"
		return retval
