
from __future__ import print_function

from PIL import VERSION, PILLOW_VERSION, _plugins

import warnings

class _imaging_not_installed:
    def __getattr__(self, id):
        raise ImportError("The _imaging C module is not installed")

try:
    __import__("FixTk")
except ImportError:
    pass

try:
    from PIL import _imaging as core
    if PILLOW_VERSION != getattr(core, 'PILLOW_VERSION', None):
         raise ImportError("The _imaging extension was built for another "
                            " version of Pillow or PIL")

except ImportError as v:
    core = _imaging_not_installed()
    if str(v).startswith("Module use of python"):
        warnings.warn(
            "The _imaging extension was built for another version "
            "of Python.",
            RuntimeWarning
            )
    elif str(v).startswith("The _imaging extension"):
        warnings.warn(str(v), RuntimeWarning)
    elif "Symbol not found: _PyUnicodeUCS2_FromString" in str(v):
        warnings.warn(
            "The _imaging extension was built for Python with UCS2 support; "
            "recompile PIL or build Python --without-wide-unicode. ",
            RuntimeWarning
            )
    elif "Symbol not found: _PyUnicodeUCS4_FromString" in str(v):
        warnings.warn(
            "The _imaging extension was built for Python with UCS4 support; "
            "recompile PIL or build Python --with-wide-unicode. ",
            RuntimeWarning
            )
    raise

try:
    import builtins
except ImportError:
    import __builtin__
    builtins = __builtin__

from PIL import ImageMode
from PIL._binary import i8, o8
from PIL._util import isPath, isStringType

import os, sys

import collections
import numbers

def isImageType(t):

    return hasattr(t, "im")

DEBUG = 0

NONE = 0

FLIP_LEFT_RIGHT = 0
FLIP_TOP_BOTTOM = 1
ROTATE_90 = 2
ROTATE_180 = 3
ROTATE_270 = 4

AFFINE = 0
EXTENT = 1
PERSPECTIVE = 2
QUAD = 3
MESH = 4

NONE = 0
NEAREST = 0
ANTIALIAS = 1
LINEAR = BILINEAR = 2
CUBIC = BICUBIC = 3

NONE = 0
NEAREST = 0
ORDERED = 1
RASTERIZE = 2
FLOYDSTEINBERG = 3

WEB = 0
ADAPTIVE = 1

MEDIANCUT = 0
MAXCOVERAGE = 1
FASTOCTREE = 2

NORMAL = 0
SEQUENCE = 1
CONTAINER = 2

if hasattr(core, 'DEFAULT_STRATEGY'):
    DEFAULT_STRATEGY = core.DEFAULT_STRATEGY
    FILTERED = core.FILTERED
    HUFFMAN_ONLY = core.HUFFMAN_ONLY
    RLE = core.RLE
    FIXED = core.FIXED

ID = []
OPEN = {}
MIME = {}
SAVE = {}
EXTENSION = {}

_MODEINFO = {

    "1": ("L", "L", ("1",)),
    "L": ("L", "L", ("L",)),
    "I": ("L", "I", ("I",)),
    "F": ("L", "F", ("F",)),
    "P": ("RGB", "L", ("P",)),
    "RGB": ("RGB", "L", ("R", "G", "B")),
    "RGBX": ("RGB", "L", ("R", "G", "B", "X")),
    "RGBA": ("RGB", "L", ("R", "G", "B", "A")),
    "CMYK": ("RGB", "L", ("C", "M", "Y", "K")),
    "YCbCr": ("RGB", "L", ("Y", "Cb", "Cr")),
    "LAB": ("RGB", "L", ("L", "A", "B")),

}

if sys.byteorder == 'little':
    _ENDIAN = '<'
else:
    _ENDIAN = '>'

_MODE_CONV = {
    "1": ('|b1', None),
    "L": ('|u1', None),
    "I": (_ENDIAN + 'i4', None),
    "F": (_ENDIAN + 'f4', None),
    "P": ('|u1', None),
    "RGB": ('|u1', 3),
    "RGBX": ('|u1', 4),
    "RGBA": ('|u1', 4),
    "CMYK": ('|u1', 4),
    "YCbCr": ('|u1', 3),
    "LAB": ('|u1', 3),
    "I;16": ('<u2', None),
    "I;16B": ('>u2', None),
    "I;16L": ('<u2', None),
    "I;16S": ('<i2', None),
    "I;16BS": ('>i2', None),
    "I;16LS": ('<i2', None),
    "I;32": ('<u4', None),
    "I;32B": ('>u4', None),
    "I;32L": ('<u4', None),
    "I;32S": ('<i4', None),
    "I;32BS": ('>i4', None),
    "I;32LS": ('<i4', None),
}

def _conv_type_shape(im):
    shape = im.size[1], im.size[0]
    typ, extra = _MODE_CONV[im.mode]
    if extra is None:
        return shape, typ
    else:
        return shape+(extra,), typ

MODES = sorted(_MODEINFO.keys())

_MAPMODES = ("L", "P", "RGBX", "RGBA", "CMYK", "I;16", "I;16L", "I;16B")

def getmodebase(mode):

    return ImageMode.getmode(mode).basemode

def getmodetype(mode):

    return ImageMode.getmode(mode).basetype

def getmodebandnames(mode):

    return ImageMode.getmode(mode).bands

def getmodebands(mode):

    return len(ImageMode.getmode(mode).bands)

_initialized = 0

def preinit():


    global _initialized
    if _initialized >= 1:
        return

    try:
        from PIL import BmpImagePlugin
    except ImportError:
        pass
    try:
        from PIL import GifImagePlugin
    except ImportError:
        pass
    try:
        from PIL import JpegImagePlugin
    except ImportError:
        pass
    try:
        from PIL import PpmImagePlugin
    except ImportError:
        pass
    try:
        from PIL import PngImagePlugin
    except ImportError:
        pass

    _initialized = 1

def init():


    global _initialized
    if _initialized >= 2:
        return 0

    for plugin in _plugins:
        try:
            if DEBUG:
                print ("Importing %s"%plugin)
            __import__("PIL.%s"%plugin, globals(), locals(), [])
        except ImportError:
            if DEBUG:
                print("Image: failed to import", end=' ')
                print(plugin, ":", sys.exc_info()[1])

    if OPEN or SAVE:
        _initialized = 2
        return 1

def _getdecoder(mode, decoder_name, args, extra=()):

    if args is None:
        args = ()
    elif not isinstance(args, tuple):
        args = (args,)

    try:
        decoder = getattr(core, decoder_name + "_decoder")
        return decoder(mode, *args + extra)
    except AttributeError:
        raise IOError("decoder %s not available" % decoder_name)

def _getencoder(mode, encoder_name, args, extra=()):

    if args is None:
        args = ()
    elif not isinstance(args, tuple):
        args = (args,)

    try:
        encoder = getattr(core, encoder_name + "_encoder")
        return encoder(mode, *args + extra)
    except AttributeError:
        raise IOError("encoder %s not available" % encoder_name)

def coerce_e(value):
    return value if isinstance(value, _E) else _E(value)

class _E:
    def __init__(self, data):
        self.data = data
    def __add__(self, other):
        return _E((self.data, "__add__", coerce_e(other).data))
    def __mul__(self, other):
        return _E((self.data, "__mul__", coerce_e(other).data))

def _getscaleoffset(expr):
    stub = ["stub"]
    data = expr(_E(stub)).data
    try:
        (a, b, c) = data
        if (a is stub and b == "__mul__" and isinstance(c, numbers.Number)):
            return c, 0.0
        if (a is stub and b == "__add__" and isinstance(c, numbers.Number)):
            return 1.0, c
    except TypeError: pass
    try:
        ((a, b, c), d, e) = data
        if (a is stub and b == "__mul__" and isinstance(c, numbers.Number) and
            d == "__add__" and isinstance(e, numbers.Number)):
            return c, e
    except TypeError: pass
    raise ValueError("illegal expression")

class Image:

    format = None
    format_description = None

    def __init__(self):
        self.im = None
        self.mode = ""
        self.size = (0, 0)
        self.palette = None
        self.info = {}
        self.category = NORMAL
        self.readonly = 0

    def _new(self, im):
        new = Image()
        new.im = im
        new.mode = im.mode
        new.size = im.size
        new.palette = self.palette
        if im.mode == "P" and not new.palette:
            from PIL import ImagePalette
            new.palette = ImagePalette.ImagePalette()
        try:
            new.info = self.info.copy()
        except AttributeError:
            new.info = {}
            for k, v in self.info:
                new.info[k] = v
        return new

    _makeself = _new

    def _copy(self):
        self.load()
        self.im = self.im.copy()
        self.readonly = 0

    def _dump(self, file=None, format=None):
        import tempfile
        if not file:
            file = tempfile.mktemp()
        self.load()
        if not format or format == "PPM":
            self.im.save_ppm(file)
        else:
            file = file + "." + format
            self.save(file, format)
        return file

    def __repr__(self):
        return "<%s.%s image mode=%s size=%dx%d at 0x%X>" % (
            self.__class__.__module__, self.__class__.__name__,
            self.mode, self.size[0], self.size[1],
            id(self)
            )

    def __getattr__(self, name):
        if name == "__array_interface__":
            new = {}
            shape, typestr = _conv_type_shape(self)
            new['shape'] = shape
            new['typestr'] = typestr
            new['data'] = self.tobytes()
            return new
        raise AttributeError(name)

    def tobytes(self, encoder_name="raw", *args):


        if len(args) == 1 and isinstance(args[0], tuple):
            args = args[0]

        if encoder_name == "raw" and args == ():
            args = self.mode

        self.load()

        e = _getencoder(self.mode, encoder_name, args)
        e.setimage(self.im)

        bufsize = max(65536, self.size[0] * 4)

        data = []
        while True:
            l, s, d = e.encode(bufsize)
            data.append(d)
            if s:
                break
        if s < 0:
            raise RuntimeError("encoder error %d in tobytes" % s)

        return b"".join(data)

    def tostring(self, *args, **kw):
        warnings.warn(
            'tostring() is deprecated. Please call tobytes() instead.',
            DeprecationWarning,
            stacklevel=2,
        )
        return self.tobytes(*args, **kw)

    def tobitmap(self, name="image"):


        self.load()
        if self.mode != "1":
            raise ValueError("not a bitmap")
        data = self.tobytes("xbm")
        return b"".join([("#define %s_width %d\n" % (name, self.size[0])).encode('ascii'),
                ("#define %s_height %d\n"% (name, self.size[1])).encode('ascii'),
                ("static char %s_bits[] = {\n" % name).encode('ascii'), data, b"};"])

    def frombytes(self, data, decoder_name="raw", *args):


        if len(args) == 1 and isinstance(args[0], tuple):
            args = args[0]

        if decoder_name == "raw" and args == ():
            args = self.mode

        d = _getdecoder(self.mode, decoder_name, args)
        d.setimage(self.im)
        s = d.decode(data)

        if s[0] >= 0:
            raise ValueError("not enough image data")
        if s[1] != 0:
            raise ValueError("cannot decode image data")

    def fromstring(self, *args, **kw):

        warnings.warn('fromstring() is deprecated. Please call frombytes() instead.', DeprecationWarning)
        return self.frombytes(*args, **kw)

    def load(self):

        if self.im and self.palette and self.palette.dirty:
            self.im.putpalette(*self.palette.getdata())
            self.palette.dirty = 0
            self.palette.mode = "RGB"
            self.palette.rawmode = None
            if "transparency" in self.info:
                if isinstance(self.info["transparency"], int):
                    self.im.putpalettealpha(self.info["transparency"], 0)
                else:
                    self.im.putpalettealphas(self.info["transparency"])
                self.palette.mode = "RGBA"

        if self.im:
            return self.im.pixel_access(self.readonly)

    def verify(self):

        pass

    def convert(self, mode=None, matrix=None, dither=None,
                palette=WEB, colors=256):


        if not mode:
            if self.mode == "P":
                self.load()
                if self.palette:
                    mode = self.palette.mode
                else:
                    mode = "RGB"
            else:
                return self.copy()

        self.load()

        if matrix:
            if mode not in ("L", "RGB"):
                raise ValueError("illegal conversion")
            im = self.im.convert_matrix(mode, matrix)
            return self._new(im)

        if mode == "P" and palette == ADAPTIVE:
            im = self.im.quantize(colors)
            return self._new(im)

        if dither is None:
            dither = FLOYDSTEINBERG

        if self.mode in ("L", "RGB") and mode == "RGBA" and "transparency" in self.info:
            return self._new(self.im.convert_transparent(mode, self.info['transparency']))

        try:
            im = self.im.convert(mode, dither)
        except ValueError:
            try:
                im = self.im.convert(getmodebase(self.mode))
                im = im.convert(mode, dither)
            except KeyError:
                raise ValueError("illegal conversion")

        return self._new(im)

    def quantize(self, colors=256, method=0, kmeans=0, palette=None):

        self.load()

        if palette:
            palette.load()
            if palette.mode != "P":
                raise ValueError("bad mode for palette image")
            if self.mode != "RGB" and self.mode != "L":
                raise ValueError(
                    "only RGB or L mode images can be quantized to a palette"
                    )
            im = self.im.convert("P", 1, palette.im)
            return self._makeself(im)

        im = self.im.quantize(colors, method, kmeans)
        return self._new(im)

    def copy(self):

        self.load()
        im = self.im.copy()
        return self._new(im)

    def crop(self, box=None):


        self.load()
        if box is None:
            return self.copy()

        return _ImageCrop(self, box)

    def draft(self, mode, size):

        pass

    def _expand(self, xmargin, ymargin=None):
        if ymargin is None:
            ymargin = xmargin
        self.load()
        return self._new(self.im.expand(xmargin, ymargin, 0))

    def filter(self, filter):


        self.load()

        if isinstance(filter, collections.Callable):
            filter = filter()
        if not hasattr(filter, "filter"):
            raise TypeError("filter argument should be ImageFilter.Filter instance or class")

        if self.im.bands == 1:
            return self._new(filter.filter(self.im))
        ims = []
        for c in range(self.im.bands):
            ims.append(self._new(filter.filter(self.im.getband(c))))
        return merge(self.mode, ims)

    def getbands(self):

        return ImageMode.getmode(self.mode).bands

    def getbbox(self):


        self.load()
        return self.im.getbbox()

    def getcolors(self, maxcolors=256):


        self.load()
        if self.mode in ("1", "L", "P"):
            h = self.im.histogram()
            out = []
            for i in range(256):
                if h[i]:
                    out.append((h[i], i))
            if len(out) > maxcolors:
                return None
            return out
        return self.im.getcolors(maxcolors)

    def getdata(self, band = None):


        self.load()
        if band is not None:
            return self.im.getband(band)
        return self.im

    def getextrema(self):


        self.load()
        if self.im.bands > 1:
            extrema = []
            for i in range(self.im.bands):
                extrema.append(self.im.getband(i).getextrema())
            return tuple(extrema)
        return self.im.getextrema()

    def getim(self):


        self.load()
        return self.im.ptr

    def getpalette(self):


        self.load()
        try:
            if bytes is str:
                return [i8(c) for c in self.im.getpalette()]
            else:
                return list(self.im.getpalette())
        except ValueError:
            return None

    def getpixel(self, xy):


        self.load()
        return self.im.getpixel(xy)

    def getprojection(self):


        self.load()
        x, y = self.im.getprojection()
        return [i8(c) for c in x], [i8(c) for c in y]

    def histogram(self, mask=None, extrema=None):

        self.load()
        if mask:
            mask.load()
            return self.im.histogram((0, 0), mask.im)
        if self.mode in ("I", "F"):
            if extrema is None:
                extrema = self.getextrema()
            return self.im.histogram(extrema)
        return self.im.histogram()

    def offset(self, xoffset, yoffset=None):

        if warnings:
            warnings.warn(
                "'offset' is deprecated; use 'ImageChops.offset' instead",
                DeprecationWarning, stacklevel=2
                )
        from PIL import ImageChops
        return ImageChops.offset(self, xoffset, yoffset)

    def paste(self, im, box=None, mask=None):


        if isImageType(box) and mask is None:
            mask = box; box = None

        if box is None:
            box = (0, 0) + self.size

        if len(box) == 2:
            if isImageType(im):
                size = im.size
            elif isImageType(mask):
                size = mask.size
            else:
                raise ValueError(
                    "cannot determine region size; use 4-item box"
                    )
            box = box + (box[0]+size[0], box[1]+size[1])

        if isStringType(im):
            from PIL import ImageColor
            im = ImageColor.getcolor(im, self.mode)

        elif isImageType(im):
            im.load()
            if self.mode != im.mode:
                if self.mode != "RGB" or im.mode not in ("RGBA", "RGBa"):
                    im = im.convert(self.mode)
            im = im.im

        self.load()
        if self.readonly:
            self._copy()

        if mask:
            mask.load()
            self.im.paste(im, box, mask.im)
        else:
            self.im.paste(im, box)

    def point(self, lut, mode=None):


        self.load()

        if isinstance(lut, ImagePointHandler):
            return lut.point(self)

        if callable(lut):
            if self.mode in ("I", "I;16", "F"):
                scale, offset = _getscaleoffset(lut)
                return self._new(self.im.point_transform(scale, offset))
            lut = [lut(i) for i in range(256)] * self.im.bands

        if self.mode == "F":
            raise ValueError("point operation not supported for this mode")

        return self._new(self.im.point(lut, mode))

    def putalpha(self, alpha):


        self.load()
        if self.readonly:
            self._copy()

        if self.mode not in ("LA", "RGBA"):
            try:
                mode = getmodebase(self.mode) + "A"
                try:
                    self.im.setmode(mode)
                except (AttributeError, ValueError):
                    im = self.im.convert(mode)
                    if im.mode not in ("LA", "RGBA"):
                        raise ValueError
                    self.im = im
                self.mode = self.im.mode
            except (KeyError, ValueError):
                raise ValueError("illegal image mode")

        if self.mode == "LA":
            band = 1
        else:
            band = 3

        if isImageType(alpha):
            if alpha.mode not in ("1", "L"):
                raise ValueError("illegal image mode")
            alpha.load()
            if alpha.mode == "1":
                alpha = alpha.convert("L")
        else:
            try:
                self.im.fillband(band, alpha)
            except (AttributeError, ValueError):
                alpha = new("L", self.size, alpha)
            else:
                return

        self.im.putband(alpha.im, band)

    def putdata(self, data, scale=1.0, offset=0.0):


        self.load()
        if self.readonly:
            self._copy()

        self.im.putdata(data, scale, offset)

    def putpalette(self, data, rawmode="RGB"):

        from PIL import ImagePalette

        if self.mode not in ("L", "P"):
            raise ValueError("illegal image mode")
        self.load()
        if isinstance(data, ImagePalette.ImagePalette):
            palette = ImagePalette.raw(data.rawmode, data.palette)
        else:
            if not isinstance(data, bytes):
                if bytes is str:
                    data = "".join(chr(x) for x in data)
                else:
                    data = bytes(data)
            palette = ImagePalette.raw(rawmode, data)
        self.mode = "P"
        self.palette = palette
        self.palette.mode = "RGB"
        self.load()

    def putpixel(self, xy, value):


        self.load()
        if self.readonly:
            self._copy()

        return self.im.putpixel(xy, value)

    def resize(self, size, resample=NEAREST):


        if resample not in (NEAREST, BILINEAR, BICUBIC, ANTIALIAS):
            raise ValueError("unknown resampling filter")

        self.load()

        if self.mode in ("1", "P"):
            resample = NEAREST

        if self.mode == 'RGBA':
            return self.convert('RGBa').resize(size, resample).convert('RGBA')

        if resample == ANTIALIAS:
            try:
                im = self.im.stretch(size, resample)
            except AttributeError:
                raise ValueError("unsupported resampling filter")
        else:
            im = self.im.resize(size, resample)

        return self._new(im)

    def rotate(self, angle, resample=NEAREST, expand=0):


        if expand:
            import math
            angle = -angle * math.pi / 180
            matrix = [
                 math.cos(angle), math.sin(angle), 0.0,
                -math.sin(angle), math.cos(angle), 0.0
                 ]
            def transform(x, y, matrix=matrix):
                (a, b, c, d, e, f) = matrix
                return a*x + b*y + c, d*x + e*y + f

            w, h = self.size
            xx = []
            yy = []
            for x, y in ((0, 0), (w, 0), (w, h), (0, h)):
                x, y = transform(x, y)
                xx.append(x)
                yy.append(y)
            w = int(math.ceil(max(xx)) - math.floor(min(xx)))
            h = int(math.ceil(max(yy)) - math.floor(min(yy)))

            x, y = transform(w / 2.0, h / 2.0)
            matrix[2] = self.size[0] / 2.0 - x
            matrix[5] = self.size[1] / 2.0 - y

            return self.transform((w, h), AFFINE, matrix, resample)

        if resample not in (NEAREST, BILINEAR, BICUBIC):
            raise ValueError("unknown resampling filter")

        self.load()

        if self.mode in ("1", "P"):
            resample = NEAREST

        return self._new(self.im.rotate(angle, resample))

    def save(self, fp, format=None, **params):


        if isPath(fp):
            filename = fp
        else:
            if hasattr(fp, "name") and isPath(fp.name):
                filename = fp.name
            else:
                filename = ""

        self.load()

        self.encoderinfo = params
        self.encoderconfig = ()

        preinit()

        ext = os.path.splitext(filename)[1].lower()

        if not format:
            try:
                format = EXTENSION[ext]
            except KeyError:
                init()
                try:
                    format = EXTENSION[ext]
                except KeyError:
                    raise KeyError(ext)

        try:
            save_handler = SAVE[format.upper()]
        except KeyError:
            init()
            save_handler = SAVE[format.upper()]

        if isPath(fp):
            fp = builtins.open(fp, "wb")
            close = 1
        else:
            close = 0

        try:
            save_handler(self, fp, filename)
        finally:
            if close:
                fp.close()

    def seek(self, frame):


        if frame != 0:
            raise EOFError

    def show(self, title=None, command=None):


        _show(self, title=title, command=command)

    def split(self):


        self.load()
        if self.im.bands == 1:
            ims = [self.copy()]
        else:
            ims = []
            for i in range(self.im.bands):
                ims.append(self._new(self.im.getband(i)))
        return tuple(ims)

    def tell(self):

        return 0

    def thumbnail(self, size, resample=NEAREST):


        x, y = self.size
        if x > size[0]: y = int(max(y * size[0] / x, 1)); x = int(size[0])
        if y > size[1]: x = int(max(x * size[1] / y, 1)); y = int(size[1])
        size = x, y

        if size == self.size:
            return

        self.draft(None, size)

        self.load()

        try:
            im = self.resize(size, resample)
        except ValueError:
            if resample != ANTIALIAS:
                raise
            im = self.resize(size, NEAREST)

        self.im = im.im
        self.mode = im.mode
        self.size = size

        self.readonly = 0

    def transform(self, size, method, data=None, resample=NEAREST, fill=1):


        if self.mode == 'RGBA':
            return self.convert('RGBa').transform(size, method, data, resample, fill).convert('RGBA')

        if isinstance(method, ImageTransformHandler):
            return method.transform(size, self, resample=resample, fill=fill)
        if hasattr(method, "getdata"):
            method, data = method.getdata()
        if data is None:
            raise ValueError("missing method data")

        im = new(self.mode, size, None)
        if method == MESH:
            for box, quad in data:
                im.__transformer(box, self, QUAD, quad, resample, fill)
        else:
            im.__transformer((0, 0)+size, self, method, data, resample, fill)

        return im

    def __transformer(self, box, image, method, data,
                      resample=NEAREST, fill=1):

        w = box[2]-box[0]
        h = box[3]-box[1]

        if method == AFFINE:
            data = (data[2], data[0], data[1],
                    data[5], data[3], data[4])
        elif method == EXTENT:
            x0, y0, x1, y1 = data
            xs = float(x1 - x0) / w
            ys = float(y1 - y0) / h
            method = AFFINE
            data = (x0 + xs/2, xs, 0, y0 + ys/2, 0, ys)
        elif method == PERSPECTIVE:
            data = (data[2], data[0], data[1],
                    data[5], data[3], data[4],
                    data[6], data[7])
        elif method == QUAD:
            nw = data[0:2]; sw = data[2:4]; se = data[4:6]; ne = data[6:8]
            x0, y0 = nw; As = 1.0 / w; At = 1.0 / h
            data = (x0, (ne[0]-x0)*As, (sw[0]-x0)*At,
                    (se[0]-sw[0]-ne[0]+x0)*As*At,
                    y0, (ne[1]-y0)*As, (sw[1]-y0)*At,
                    (se[1]-sw[1]-ne[1]+y0)*As*At)
        else:
            raise ValueError("unknown transformation method")

        if resample not in (NEAREST, BILINEAR, BICUBIC):
            raise ValueError("unknown resampling filter")

        image.load()

        self.load()

        if image.mode in ("1", "P"):
            resample = NEAREST

        self.im.transform2(box, image.im, method, data, resample, fill)

    def transpose(self, method):


        self.load()
        im = self.im.transpose(method)
        return self._new(im)

class _ImageCrop(Image):

    def __init__(self, im, box):

        Image.__init__(self)

        x0, y0, x1, y1 = box
        if x1 < x0:
            x1 = x0
        if y1 < y0:
            y1 = y0

        self.mode = im.mode
        self.size = x1-x0, y1-y0

        self.__crop = x0, y0, x1, y1

        self.im = im.im

    def load(self):

        if self.__crop:
            self.im = self.im.crop(self.__crop)
            self.__crop = None

        if self.im:
            return self.im.pixel_access(self.readonly)

class ImagePointHandler:
    pass

class ImageTransformHandler:
    pass

def _wedge():


    return Image()._new(core.wedge("L"))

def new(mode, size, color=0):


    if color is None:
        return Image()._new(core.new(mode, size))

    if isStringType(color):

        from PIL import ImageColor
        color = ImageColor.getcolor(color, mode)

    return Image()._new(core.fill(mode, size, color))

def frombytes(mode, size, data, decoder_name="raw", *args):


    if len(args) == 1 and isinstance(args[0], tuple):
        args = args[0]

    if decoder_name == "raw" and args == ():
        args = mode

    im = new(mode, size)
    im.frombytes(data, decoder_name, args)
    return im

def fromstring(*args, **kw):

    warnings.warn(
        'fromstring() is deprecated. Please call frombytes() instead.',
        DeprecationWarning,
        stacklevel=2
    )
    return frombytes(*args, **kw)

def frombuffer(mode, size, data, decoder_name="raw", *args):


    if len(args) == 1 and isinstance(args[0], tuple):
        args = args[0]

    if decoder_name == "raw":
        if args == ():
            if warnings:
                warnings.warn(
                    "the frombuffer defaults may change in a future release; "
                    "for portability, change the call to read:\n"
                    "  frombuffer(mode, size, data, 'raw', mode, 0, 1)",
                    RuntimeWarning, stacklevel=2
                )
            args = mode, 0, -1
        if args[0] in _MAPMODES:
            im = new(mode, (1,1))
            im = im._new(
                core.map_buffer(data, size, decoder_name, None, 0, args)
                )
            im.readonly = 1
            return im

    return frombytes(mode, size, data, decoder_name, args)

def fromarray(obj, mode=None):

    arr = obj.__array_interface__
    shape = arr['shape']
    ndim = len(shape)
    try:
        strides = arr['strides']
    except KeyError:
        strides = None
    if mode is None:
        try:
            typekey = (1, 1) + shape[2:], arr['typestr']
            mode, rawmode = _fromarray_typemap[typekey]
        except KeyError:
            raise TypeError("Cannot handle this data type")
    else:
        rawmode = mode
    if mode in ["1", "L", "I", "P", "F"]:
        ndmax = 2
    elif mode == "RGB":
        ndmax = 3
    else:
        ndmax = 4
    if ndim > ndmax:
        raise ValueError("Too many dimensions.")

    size = shape[1], shape[0]
    if strides is not None:
        if hasattr(obj, 'tobytes'):
            obj = obj.tobytes()
        else:
            obj = obj.tostring()

    return frombuffer(mode, size, obj, "raw", rawmode, 0, 1)

_fromarray_typemap = {
    ((1, 1), "|u1"): ("L", "L"),
    ((1, 1), "|i1"): ("I", "I;8"),
    ((1, 1), "<i2"): ("I", "I;16"),
    ((1, 1), ">i2"): ("I", "I;16B"),
    ((1, 1), "<i4"): ("I", "I;32"),
    ((1, 1), ">i4"): ("I", "I;32B"),
    ((1, 1), "<f4"): ("F", "F;32F"),
    ((1, 1), ">f4"): ("F", "F;32BF"),
    ((1, 1), "<f8"): ("F", "F;64F"),
    ((1, 1), ">f8"): ("F", "F;64BF"),
    ((1, 1, 3), "|u1"): ("RGB", "RGB"),
    ((1, 1, 4), "|u1"): ("RGBA", "RGBA"),
    }

_fromarray_typemap[((1, 1), _ENDIAN + "i4")] = ("I", "I")
_fromarray_typemap[((1, 1), _ENDIAN + "f4")] = ("F", "F")

def open(fp, mode="r"):


    if mode != "r":
        raise ValueError("bad mode")

    if isPath(fp):
        filename = fp
        fp = builtins.open(fp, "rb")
    else:
        filename = ""

    prefix = fp.read(16)

    preinit()

    for i in ID:
        try:
            factory, accept = OPEN[i]
            if not accept or accept(prefix):
                fp.seek(0)
                return factory(fp, filename)
        except (SyntaxError, IndexError, TypeError):
            pass

    if init():

        for i in ID:
            try:
                factory, accept = OPEN[i]
                if not accept or accept(prefix):
                    fp.seek(0)
                    return factory(fp, filename)
            except (SyntaxError, IndexError, TypeError):
                pass

    raise IOError("cannot identify image file")

def alpha_composite(im1, im2):


    im1.load()
    im2.load()
    return im1._new(core.alpha_composite(im1.im, im2.im))

def blend(im1, im2, alpha):


    im1.load()
    im2.load()
    return im1._new(core.blend(im1.im, im2.im, alpha))

def composite(image1, image2, mask):


    image = image2.copy()
    image.paste(image1, None, mask)
    return image

def eval(image, *args):


    return image.point(args[0])

def merge(mode, bands):


    if getmodebands(mode) != len(bands) or "*" in mode:
        raise ValueError("wrong number of bands")
    for im in bands[1:]:
        if im.mode != getmodetype(mode):
            raise ValueError("mode mismatch")
        if im.size != bands[0].size:
            raise ValueError("size mismatch")
    im = core.new(mode, bands[0].size)
    for i in range(getmodebands(mode)):
        bands[i].load()
        im.putband(bands[i].im, i)
    return bands[0]._new(im)

def register_open(id, factory, accept=None):

    id = id.upper()
    ID.append(id)
    OPEN[id] = factory, accept

def register_mime(id, mimetype):

    MIME[id.upper()] = mimetype

def register_save(id, driver):

    SAVE[id.upper()] = driver

def register_extension(id, extension):

    EXTENSION[extension.lower()] = id.upper()

def _show(image, **options):
    _showxv(image, **options)

def _showxv(image, title=None, **options):
    from PIL import ImageShow
    ImageShow.show(image, title, **options)
