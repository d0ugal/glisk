import {
    forwardRef,
    useEffect,
    useImperativeHandle,
    useRef,
} from "react";
import * as d3 from "d3";
import type { TreeNode } from "../types";

// Arc coordinates we animate between (a mutable copy of the partition layout).
interface Arc {
    x0: number;
    x1: number;
    y0: number;
    y1: number;
}

// Our hierarchy nodes carry the live/target arc plus a precomputed colour.
export type SBNode = d3.HierarchyRectangularNode<TreeNode> & {
    current: Arc;
    target?: Arc;
    _color?: string;
};

export interface SunburstHandle {
    focusTo: (node: SBNode) => void;
}

interface SunburstProps {
    data: TreeNode;
    onHover: (node: SBNode | null) => void;
    onFocus: (node: SBNode) => void;
    // focusPath (folder names below the root) is restored after a rebuild so
    // an in-place tree update — e.g. a single-folder rescan — keeps the user
    // looking at the same folder instead of snapping back to the root.
    focusPath?: string[];
}

// nodeByPath descends the hierarchy following folder names.
function nodeByPath(root: SBNode, segments: string[]): SBNode | null {
    let cur: SBNode = root;
    for (const seg of segments) {
        const kids = (cur.children ?? []) as SBNode[];
        const next = kids.find((c) => c.data.name === seg);
        if (!next) return null;
        cur = next;
    }
    return cur;
}

const SIZE = 820;
const RINGS = 3; // visible depth from the centre
const radius = SIZE / 2 / (RINGS + 1);

// DaisyDisk's signature: every top-level folder owns a hue and its descendants
// are tints of that hue, with a little sibling spread so adjacent arcs read
// as distinct rather than one flat band.
function paintTree(root: SBNode) {
    const tops = (root.children ?? []) as SBNode[];
    tops.forEach((top, i) => {
        const hue = ((i + 0.5) / Math.max(tops.length, 1)) * 360;
        paint(top, hue, 1);
    });

    function paint(node: SBNode, hue: number, depth: number) {
        const light = Math.max(34, 72 - depth * 8);
        const sat = Math.max(38, 68 - depth * 3);
        node._color = `hsl(${hue.toFixed(1)} ${sat}% ${light}%)`;
        const kids = (node.children ?? []) as SBNode[];
        const spread = Math.min(34, 16 * Math.log2(kids.length + 1));
        kids.forEach((c, j) => {
            const offset =
                kids.length > 1 ? (j / (kids.length - 1) - 0.5) * spread : 0;
            paint(c, hue + offset, depth + 1);
        });
    }
}

export const Sunburst = forwardRef<SunburstHandle, SunburstProps>(
    function Sunburst({ data, onHover, onFocus, focusPath }, ref) {
        const hostRef = useRef<HTMLDivElement>(null);
        const zoomRef = useRef<(p: SBNode, animate?: boolean) => void>(
            () => {},
        );
        // Read inside the build effect (which depends only on `data`) without
        // making focusPath a dependency that would force a rebuild.
        const focusPathRef = useRef(focusPath);
        focusPathRef.current = focusPath;

        useImperativeHandle(ref, () => ({
            focusTo: (node: SBNode) => zoomRef.current(node),
        }));

        useEffect(() => {
            const host = hostRef.current;
            if (!host) return;
            host.replaceChildren();

            const hierarchy = d3
                .hierarchy<TreeNode>(data)
                .sum((d) => (d.children && d.children.length ? 0 : d.size))
                .sort((a, b) => (b.value ?? 0) - (a.value ?? 0));

            const root = d3
                .partition<TreeNode>()
                .size([2 * Math.PI, hierarchy.height + 1])(hierarchy) as SBNode;

            root.each((d) => ((d as SBNode).current = d as unknown as Arc));
            paintTree(root);

            const arc = d3
                .arc<Arc>()
                .startAngle((d) => d.x0)
                .endAngle((d) => d.x1)
                .padAngle((d) => Math.min((d.x1 - d.x0) / 2, 0.004))
                .padRadius(radius * 1.5)
                .innerRadius((d) => d.y0 * radius)
                .outerRadius((d) => Math.max(d.y0 * radius, d.y1 * radius - 1.5))
                .cornerRadius(3);

            const svg = d3
                .select(host)
                .append("svg")
                .attr("viewBox", `${-SIZE / 2} ${-SIZE / 2} ${SIZE} ${SIZE}`)
                .attr("preserveAspectRatio", "xMidYMid meet")
                // Sizing is handled in CSS (responsive square that fits the
                // stage) so the circle's centre stays at the stage centre,
                // where the read-out is positioned.
                .style("font-family", "'Hanken Grotesk', sans-serif");

            // Liquid-jelly filter: fractal-noise turbulence displaces the arc
            // edges. At rest the displacement scale is 0 (and the filter is
            // detached) so shapes are crisp; during a zoom it ripples and
            // settles. A unique id avoids clashes across mounts.
            const fid = `jelly-${Math.random().toString(36).slice(2, 8)}`;
            const filter = svg
                .append("defs")
                .append("filter")
                .attr("id", fid)
                .attr("x", "-30%")
                .attr("y", "-30%")
                .attr("width", "160%")
                .attr("height", "160%");
            const turb = filter
                .append("feTurbulence")
                .attr("type", "fractalNoise")
                .attr("baseFrequency", 0.012)
                .attr("numOctaves", 2)
                .attr("seed", Math.floor(Math.random() * 100))
                .attr("result", "noise");
            const disp = filter
                .append("feDisplacementMap")
                .attr("in", "SourceGraphic")
                .attr("in2", "noise")
                .attr("scale", 0)
                .attr("xChannelSelector", "R")
                .attr("yChannelSelector", "G");

            const arcVisible = (d: Arc) =>
                d.y1 <= RINGS && d.y0 >= 1 && d.x1 > d.x0;
            const labelVisible = (d: Arc) =>
                d.y1 <= RINGS &&
                d.y0 >= 1 &&
                (d.y1 - d.y0) * (d.x1 - d.x0) > 0.045;
            const labelTransform = (d: Arc) => {
                const x = (((d.x0 + d.x1) / 2) * 180) / Math.PI;
                const y = ((d.y0 + d.y1) / 2) * radius;
                return `rotate(${x - 90}) translate(${y},0) rotate(${
                    x < 180 ? 0 : 180
                })`;
            };

            const nodes = root.descendants().slice(1) as SBNode[];

            const pathG = svg.append("g");
            const path = pathG
                .selectAll<SVGPathElement, SBNode>("path")
                .data(nodes)
                .join("path")
                .attr("fill", (d) => d._color ?? "#555")
                .attr("fill-opacity", (d) =>
                    arcVisible(d.current) ? (d.children ? 0.85 : 0.7) : 0,
                )
                .attr("stroke", "#070809")
                .attr("stroke-width", 0.75)
                .attr("pointer-events", (d) =>
                    arcVisible(d.current) ? "auto" : "none",
                )
                .attr("d", (d) => arc(d.current))
                .style("cursor", (d) => (d.children ? "pointer" : "default"));

            path.on("mouseenter", (_e, d) => {
                onHover(d);
                d3.select<SVGPathElement, SBNode>(
                    _e.currentTarget as SVGPathElement,
                )
                    .attr("fill-opacity", 1)
                    .attr("stroke", "#fff8ee")
                    .attr("stroke-width", 1.25);
            }).on("mouseleave", (_e, d) => {
                onHover(null);
                d3.select<SVGPathElement, SBNode>(
                    _e.currentTarget as SVGPathElement,
                )
                    .attr("fill-opacity", () =>
                        arcVisible(d.current) ? (d.children ? 0.85 : 0.7) : 0,
                    )
                    .attr("stroke", "#070809")
                    .attr("stroke-width", 0.75);
            });

            path.filter((d) => !!d.children).on("click", (_e, d) => zoom(d));

            const label = svg
                .append("g")
                .attr("pointer-events", "none")
                .attr("text-anchor", "middle")
                .style("user-select", "none")
                .selectAll<SVGTextElement, SBNode>("text")
                .data(nodes)
                .join("text")
                .attr("dy", "0.35em")
                .attr("fill", "#0c0d10")
                .attr("font-size", 10)
                .attr("font-weight", 600)
                .attr("fill-opacity", (d) => (labelVisible(d.current) ? 0.9 : 0))
                .attr("transform", (d) => labelTransform(d.current))
                .text((d) => d.data.name);

            // Centre hit-target: click to zoom out one level.
            const center = svg
                .append("circle")
                .datum(root)
                .attr("r", radius)
                .attr("fill", "transparent")
                .attr("pointer-events", "all")
                .style("cursor", "pointer")
                .on("click", (_e, d) => zoom(d as SBNode));

            function zoom(p: SBNode, animate = true) {
                center.datum(p.parent ? (p.parent as SBNode) : root);
                onFocus(p);

                // Ripple the edges: attach the filter, run a damped sine on the
                // displacement scale (a few wobbles fading to nothing) plus a
                // little drift on the noise frequency, then detach so the arcs
                // are crisp at rest.
                if (animate) {
                    pathG.attr("filter", `url(#${fid})`);
                    disp.interrupt()
                        .attr("scale", 0)
                        .transition()
                        .duration(1800)
                        .attrTween(
                            "scale",
                            () => (tt) =>
                                String(
                                    16 *
                                        Math.sin(tt * Math.PI * 3.2) *
                                        (1 - tt) ** 1.5,
                                ),
                        )
                        .on("end interrupt", () => pathG.attr("filter", null));
                    turb.interrupt()
                        .attr("baseFrequency", 0.03)
                        .transition()
                        .duration(1800)
                        .attrTween(
                            "baseFrequency",
                            () => (tt) => String(0.03 - 0.02 * tt),
                        );
                }

                root.each((d) => {
                    const n = d as SBNode;
                    n.target = {
                        x0:
                            Math.max(
                                0,
                                Math.min(1, (n.x0 - p.x0) / (p.x1 - p.x0)),
                            ) *
                            2 *
                            Math.PI,
                        x1:
                            Math.max(
                                0,
                                Math.min(1, (n.x1 - p.x0) / (p.x1 - p.x0)),
                            ) *
                            2 *
                            Math.PI,
                        y0: Math.max(0, n.y0 - p.depth),
                        y1: Math.max(0, n.y1 - p.depth),
                    };
                });

                // Jelly settle: an elastic ease-out makes the arcs overshoot
                // and wobble briefly before snapping firm at the end. Restores
                // (animate=false) jump instantly with no wobble.
                // eslint-disable-next-line @typescript-eslint/no-explicit-any
                const t = svg
                    .transition()
                    .duration(animate ? 1800 : 0)
                    .ease(
                        animate
                            ? d3.easeElasticOut.amplitude(1).period(0.55)
                            : d3.easeLinear,
                    ) as any;

                path.transition(t)
                    .tween("data", (d) => {
                        const i = d3.interpolate(d.current, d.target!);
                        return (tt: number) => (d.current = i(tt));
                    })
                    .filter(function (d) {
                        return (
                            +(this.getAttribute("fill-opacity") ?? 0) > 0 ||
                            arcVisible(d.target!)
                        );
                    })
                    .attr("fill-opacity", (d) =>
                        arcVisible(d.target!) ? (d.children ? 0.85 : 0.7) : 0,
                    )
                    .attr("pointer-events", (d) =>
                        arcVisible(d.target!) ? "auto" : "none",
                    )
                    .attrTween("d", (d) => () => arc(d.current) ?? "");

                label
                    .filter(function (d) {
                        return (
                            +(this.getAttribute("fill-opacity") ?? 0) > 0 ||
                            labelVisible(d.target!)
                        );
                    })
                    .transition(t)
                    .attr("fill-opacity", (d) =>
                        labelVisible(d.target!) ? 0.9 : 0,
                    )
                    .attrTween(
                        "transform",
                        (d) => () => labelTransform(d.current),
                    );
            }

            zoomRef.current = zoom;
            onFocus(root);
            onHover(null);

            // Restore the previously-viewed folder after an in-place rebuild.
            const restore = focusPathRef.current;
            if (restore && restore.length) {
                const target = nodeByPath(root, restore);
                if (target) zoom(target, false);
            }

            return () => {
                host.replaceChildren();
            };
        }, [data, onHover, onFocus]);

        return <div className="sunburst" ref={hostRef} />;
    },
);
