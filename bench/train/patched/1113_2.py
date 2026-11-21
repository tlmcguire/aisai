

import heapq
import inspect
import subprocess
from abc import ABCMeta, abstractmethod
from collections import defaultdict
from contextlib import contextmanager, suppress
from inspect import signature
from warnings import warn

import numpy as np

from astropy import units as u
from astropy.utils.exceptions import AstropyWarning

__all__ = [
    "TransformGraph",
    "CoordinateTransform",
    "FunctionTransform",
    "BaseAffineTransform",
    "AffineTransform",
    "StaticMatrixTransform",
    "DynamicMatrixTransform",
    "FunctionTransformWithFiniteDifference",
    "CompositeTransform",
]

def frame_attrs_from_set(frame_set):

    result = {}

    for frame_cls in frame_set:
        result.update(frame_cls.frame_attributes)

    return result

def frame_comps_from_set(frame_set):

    result = set()

    for frame_cls in frame_set:
        rep_info = frame_cls._frame_specific_representation_info
        for mappings in rep_info.values():
            for rep_map in mappings:
                result.update([rep_map.framename])

    return result

class TransformGraph:


    def __init__(self):
        self._graph = defaultdict(dict)
        self.invalidate_cache()

    @property
    def _cached_names(self):
        if self._cached_names_dct is None:
            self._cached_names_dct = dct = {}
            for c in self.frame_set:
                nm = getattr(c, "name", None)
                if nm is not None:
                    if not isinstance(nm, list):
                        nm = [nm]
                    for name in nm:
                        dct[name] = c

        return self._cached_names_dct

    @property
    def frame_set(self):

        if self._cached_frame_set is None:
            self._cached_frame_set = set()
            for a in self._graph:
                self._cached_frame_set.add(a)
                for b in self._graph[a]:
                    self._cached_frame_set.add(b)

        return self._cached_frame_set.copy()

    @property
    def frame_attributes(self):

        if self._cached_frame_attributes is None:
            self._cached_frame_attributes = frame_attrs_from_set(self.frame_set)

        return self._cached_frame_attributes

    @property
    def frame_component_names(self):

        if self._cached_component_names is None:
            self._cached_component_names = frame_comps_from_set(self.frame_set)

        return self._cached_component_names

    def invalidate_cache(self):

        self._cached_names_dct = None
        self._cached_frame_set = None
        self._cached_frame_attributes = None
        self._cached_component_names = None
        self._shortestpaths = {}
        self._composite_cache = {}

    def add_transform(self, fromsys, tosys, transform):

        if not inspect.isclass(fromsys):
            raise TypeError("fromsys must be a class")
        if not inspect.isclass(tosys):
            raise TypeError("tosys must be a class")
        if not callable(transform):
            raise TypeError("transform must be callable")

        frame_set = self.frame_set.copy()
        frame_set.add(fromsys)
        frame_set.add(tosys)

        attrs = set(frame_attrs_from_set(frame_set).keys())
        comps = frame_comps_from_set(frame_set)

        invalid_attrs = attrs.intersection(comps)
        if invalid_attrs:
            invalid_frames = set()
            for attr in invalid_attrs:
                if attr in fromsys.frame_attributes:
                    invalid_frames.update([fromsys])

                if attr in tosys.frame_attributes:
                    invalid_frames.update([tosys])

            raise ValueError(
                f"Frame(s) {list(invalid_frames)} contain invalid attribute names:"
                f" {invalid_attrs}\nFrame attributes can not conflict with *any* of"
                " the frame data component names (see"
                " `frame_transform_graph.frame_component_names`)."
            )

        self._graph[fromsys][tosys] = transform
        self.invalidate_cache()

    def remove_transform(self, fromsys, tosys, transform):

        if fromsys is None or tosys is None:
            if not (tosys is None and fromsys is None):
                raise ValueError("fromsys and tosys must both be None if either are")
            if transform is None:
                raise ValueError("cannot give all Nones to remove_transform")

            for a in self._graph:
                agraph = self._graph[a]
                for b in agraph:
                    if agraph[b] is transform:
                        del agraph[b]
                        fromsys = a
                        break

                if fromsys:
                    break
            else:
                raise ValueError(f"Could not find transform {transform} in the graph")

        else:
            if transform is None:
                self._graph[fromsys].pop(tosys, None)
            else:
                curr = self._graph[fromsys].get(tosys, None)
                if curr is transform:
                    self._graph[fromsys].pop(tosys)
                else:
                    raise ValueError(
                        f"Current transform from {fromsys} to {tosys} is not"
                        f" {transform}"
                    )

        if self._graph[fromsys] == {}:
            self._graph.pop(fromsys)

        self.invalidate_cache()

    def find_shortest_path(self, fromsys, tosys):

        inf = float("inf")

        if tosys is fromsys:
            if tosys not in self._graph[fromsys]:
                return [tosys], 0
        if tosys in self._graph[fromsys]:
            t = self._graph[fromsys][tosys]
            return [fromsys, tosys], float(t.priority if hasattr(t, "priority") else 1)

        if fromsys in self._shortestpaths:
            fpaths = self._shortestpaths[fromsys]
            if tosys in fpaths:
                return fpaths[tosys]
            else:
                return None, inf

        nodes = []
        for a in self._graph:
            if a not in nodes:
                nodes.append(a)
            for b in self._graph[a]:
                if b not in nodes:
                    nodes.append(b)

        if fromsys not in nodes or tosys not in nodes:
            return None, inf

        edgeweights = {}
        for a in self._graph:
            edgeweights[a] = aew = {}
            agraph = self._graph[a]
            for b in agraph:
                aew[b] = float(getattr(agraph[b], "priority", 1))

        q = [[inf, i, n, []] for i, n in enumerate(nodes) if n is not fromsys]
        q.insert(0, [0, -1, fromsys, []])

        result = {}

        while len(q) > 0:
            d, orderi, n, path = heapq.heappop(q)

            if d == inf:
                result[n] = (None, d)
                for d, orderi, n, path in q:
                    result[n] = (None, d)
                break
            else:
                result[n] = (path, d)
                path.append(n)
                if n not in edgeweights:
                    continue
                for n2 in edgeweights[n]:
                    if n2 not in result:
                        for i in range(len(q)):
                            if q[i][2] == n2:
                                break
                        else:
                            raise ValueError(
                                "n2 not in heap - this should be impossible!"
                            )

                        newd = d + edgeweights[n][n2]
                        if newd < q[i][0]:
                            q[i][0] = newd
                            q[i][3] = list(path)
                            heapq.heapify(q)

        self._shortestpaths[fromsys] = result
        return result[tosys]

    def get_transform(self, fromsys, tosys):

        if not inspect.isclass(fromsys):
            raise TypeError("fromsys is not a class")
        if not inspect.isclass(tosys):
            raise TypeError("tosys is not a class")

        path, distance = self.find_shortest_path(fromsys, tosys)

        if path is None:
            return None

        transforms = []
        currsys = fromsys
        for p in path[1:]:
            transforms.append(self._graph[currsys][p])
            currsys = p

        fttuple = (fromsys, tosys)
        if fttuple not in self._composite_cache:
            comptrans = CompositeTransform(
                transforms, fromsys, tosys, register_graph=False
            )
            self._composite_cache[fttuple] = comptrans
        return self._composite_cache[fttuple]

    def lookup_name(self, name):

        return self._cached_names.get(name, None)

    def get_names(self):

        return list(self._cached_names.keys())

    def to_dot_graph(
        self,
        priorities=True,
        addnodes=[],
        savefn=None,
        savelayout="plain",
        saveformat=None,
        color_edges=True,
    ):

        nodes = []
        for a in self._graph:
            if a not in nodes:
                nodes.append(a)
            for b in self._graph[a]:
                if b not in nodes:
                    nodes.append(b)
        for node in addnodes:
            if node not in nodes:
                nodes.append(node)
        nodenames = []
        invclsaliases = {
            f: [k for k, v in self._cached_names.items() if v == f]
            for f in self.frame_set
        }
        for n in nodes:
            if n in invclsaliases:
                aliases = "`\\n`".join(invclsaliases[n])
                nodenames.append(
                    '{0} [shape=oval label="{0}\\n`{1}`"]'.format(n.__name__, aliases)
                )
            else:
                nodenames.append(n.__name__ + "[ shape=oval ]")

        edgenames = []
        for a in self._graph:
            agraph = self._graph[a]
            for b in agraph:
                transform = agraph[b]
                pri = transform.priority if hasattr(transform, "priority") else 1
                color = trans_to_color[transform.__class__] if color_edges else "black"
                edgenames.append((a.__name__, b.__name__, pri, color))

        lines = ["digraph AstropyCoordinateTransformGraph {"]
        lines.append("graph [rankdir=LR]")
        lines.append("; ".join(nodenames) + ";")
        for enm1, enm2, weights, color in edgenames:
            labelstr_fmt = "[ {0} {1} ]"

            if priorities:
                priority_part = f'label = "{weights}"'
            else:
                priority_part = ""

            color_part = f'color = "{color}"'

            labelstr = labelstr_fmt.format(priority_part, color_part)
            lines.append(f"{enm1} -> {enm2}{labelstr};")

        lines.append("")
        lines.append("overlap=false")
        lines.append("}")
        dotgraph = "\n".join(lines)

        if savefn is not None:
            if savelayout == "plain":
                with open(savefn, "w") as f:
                    f.write(dotgraph)
            elif savelayout in (
                "dot",
                "neato",
                "fdp",
                "sfdp",
                "circo",
                "twopi",
                "nop",
                "nop2",
                "osage",
                "patchwork",
            ):
                args = [savelayout]
                if saveformat is not None:
                    args.append("-T" + saveformat)
                proc = subprocess.Popen(
                    args,
                    stdin=subprocess.PIPE,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    encoding='utf-8'
                )
                stdout, stderr = proc.communicate(input=dotgraph)
                if proc.returncode != 0:
                    raise OSError("problem running graphviz: \n" + stderr)

                with open(savefn, "w") as f:
                    f.write(stdout)
            else:
                raise NotImplementedError(f'savelayout="{savelayout}" is not supported')

        return dotgraph

    def to_networkx_graph(self):

        import networkx as nx

        nxgraph = nx.Graph()

        for a in self._graph:
            if a not in nxgraph:
                nxgraph.add_node(a)
            for b in self._graph[a]:
                if b not in nxgraph:
                    nxgraph.add_node(b)

        for a in self._graph:
            agraph = self._graph[a]
            for b in agraph:
                transform = agraph[b]
                pri = transform.priority if hasattr(transform, "priority") else 1
                color = trans_to_color[transform.__class__]
                nxgraph.add_edge(a, b, weight=pri, color=color)

        return nxgraph

    def transform(self, transcls, fromsys, tosys, priority=1, **kwargs):


        def deco(func):
            transcls(
                func, fromsys, tosys, priority=priority, register_graph=self, **kwargs
            )
            return func

        return deco

    def _add_merged_transform(self, fromsys, tosys, *furthersys, priority=1):

        frames = [fromsys, tosys, *furthersys]
        lastsys = frames[-1]
        full_path = self.get_transform(fromsys, lastsys)
        transforms = [
            self.get_transform(frame_a, frame_b)
            for frame_a, frame_b in zip(frames[:-1], frames[1:])
        ]
        if None in transforms:
            raise ValueError("This transformation path is not possible")
        if len(full_path.transforms) == 1:
            raise ValueError(
                f"A direct transform for {fromsys.__name__}->{lastsys.__name__} already"
                " exists"
            )

        self.add_transform(
            fromsys,
            lastsys,
            CompositeTransform(
                transforms, fromsys, lastsys, priority=priority
            )._as_single_transform(),
        )

    @contextmanager
    def impose_finite_difference_dt(self, dt):

        key = "finite_difference_dt"
        saved_settings = []

        try:
            for to_frames in self._graph.values():
                for transform in to_frames.values():
                    if hasattr(transform, key):
                        old_setting = (transform, key, getattr(transform, key))
                        saved_settings.append(old_setting)
                        setattr(transform, key, dt)
            yield
        finally:
            for setting in saved_settings:
                setattr(*setting)

class CoordinateTransform(metaclass=ABCMeta):


    def __init__(self, fromsys, tosys, priority=1, register_graph=None):
        if not inspect.isclass(fromsys):
            raise TypeError("fromsys must be a class")
        if not inspect.isclass(tosys):
            raise TypeError("tosys must be a class")

        self.fromsys = fromsys
        self.tosys = tosys
        self.priority = float(priority)

        if register_graph:
            self.register(register_graph)
        else:
            if not inspect.isclass(fromsys) or not inspect.isclass(tosys):
                raise TypeError("fromsys and tosys must be classes")

        self.overlapping_frame_attr_names = overlap = []
        if hasattr(fromsys, "frame_attributes") and hasattr(tosys, "frame_attributes"):
            for from_nm in fromsys.frame_attributes:
                if from_nm in tosys.frame_attributes:
                    overlap.append(from_nm)

    def register(self, graph):

        graph.add_transform(self.fromsys, self.tosys, self)

    def unregister(self, graph):

        graph.remove_transform(self.fromsys, self.tosys, self)

    @abstractmethod
    def __call__(self, fromcoord, toframe):


class FunctionTransform(CoordinateTransform):


    def __init__(self, func, fromsys, tosys, priority=1, register_graph=None):
        if not callable(func):
            raise TypeError("func must be callable")

        with suppress(TypeError):
            sig = signature(func)
            kinds = [x.kind for x in sig.parameters.values()]
            if (
                len(x for x in kinds if x == sig.POSITIONAL_ONLY) != 2
                and sig.VAR_POSITIONAL not in kinds
            ):
                raise ValueError("provided function does not accept two arguments")

        self.func = func

        super().__init__(
            fromsys, tosys, priority=priority, register_graph=register_graph
        )
