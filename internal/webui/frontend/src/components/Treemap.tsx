import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import * as d3 from "d3";
import type { TreeNode } from "../types";
import {
    nodeByPath,
    paintTree,
    type SBNode,
    type SunburstHandle,
} from "./Sunburst";

interface TreemapProps {
    data: TreeNode;
    onHover: (node: SBNode | null) => void;
    onFocus: (node: SBNode) => void;
    focusPath?: string[];
}

const LEVELS = 3; // visible depth below the folder in view
const HEADER = 16; // folder label strip; also the click target for a parent

export const Treemap = forwardRef<SunburstHandle, TreemapProps>(
    function Treemap({ data, onHover, onFocus, focusPath }, ref) {
        const hostRef = useRef<HTMLDivElement>(null);
        const zoomRef = useRef<(p: SBNode) => void>(() => {});
        const focusPathRef = useRef(focusPath);
        focusPathRef.current = focusPath;

        useImperativeHandle(ref, () => ({
            focusTo: (node: SBNode) => zoomRef.current(node),
        }));

        useEffect(() => {
            const host = hostRef.current;
            if (!host) return;

            const root = d3
                .hierarchy<TreeNode>(data)
                .sum((d) => (d.children && d.children.length ? 0 : d.size))
                .sort((a, b) => (b.value ?? 0) - (a.value ?? 0)) as SBNode;
            paintTree(root);

            let focus = root;
            const restore = focusPathRef.current;
            if (restore && restore.length) focus = nodeByPath(root, restore) ?? root;

            function render(p: SBNode) {
                host!.replaceChildren();
                const w = Math.max(host!.clientWidth, 200);
                const h = Math.max(host!.clientHeight, 200);

                // d3.treemap indexes padding by depth, so lay p out as depth 0.
                const base = p.depth;
                p.each((d) => ((d as { depth: number }).depth -= base));
                d3
                    .treemap<TreeNode>()
                    .tile(d3.treemapSquarify)
                    .size([w, h])
                    .paddingOuter(2)
                    .paddingInner(1)
                    .paddingTop((d) =>
                        d.depth > 0 && d.depth < LEVELS ? HEADER : 2,
                    )
                    .round(true)(p);
                p.each((d) => ((d as { depth: number }).depth += base));

                const nodes = (p.descendants() as SBNode[]).filter(
                    (d) =>
                        d !== p &&
                        d.depth <= p.depth + LEVELS &&
                        d.x1 - d.x0 >= 1 &&
                        d.y1 - d.y0 >= 1,
                );

                const svg = d3
                    .select(host)
                    .append("svg")
                    .attr("width", w)
                    .attr("height", h)
                    .attr("viewBox", `0 0 ${w} ${h}`)
                    .style("font-family", "'Hanken Grotesk', sans-serif");

                svg.append("rect")
                    .attr("width", w)
                    .attr("height", h)
                    .attr("fill", "transparent")
                    .style("cursor", p.parent ? "zoom-out" : "default")
                    .on("click", () => p.parent && zoom(p.parent as SBNode));

                const cell = svg
                    .selectAll<SVGGElement, SBNode>("g.cell")
                    .data(nodes)
                    .join("g")
                    .attr("class", "cell")
                    .attr("transform", (d) => `translate(${d.x0},${d.y0})`)
                    .style("cursor", (d) => (d.children ? "pointer" : "default"));

                cell.append("rect")
                    .attr("width", (d) => d.x1 - d.x0)
                    .attr("height", (d) => d.y1 - d.y0)
                    .attr("rx", 2)
                    .attr("fill", (d) => d._color ?? "#555")
                    .attr("fill-opacity", (d) => (d.children ? 0.85 : 0.7))
                    .attr("stroke", "#070809")
                    .attr("stroke-width", 0.75);

                const clip = `tm-${Math.random().toString(36).slice(2, 8)}`;
                cell.append("clipPath")
                    .attr("id", (_d, i) => `${clip}-${i}`)
                    .append("rect")
                    .attr("width", (d) => Math.max(0, d.x1 - d.x0 - 4))
                    .attr("height", (d) => d.y1 - d.y0);

                // One text per cell, so indices match the clipPaths.
                cell.append("text")
                    .attr("clip-path", (_d, i) => `url(#${clip}-${i})`)
                    .attr("x", 4)
                    .attr("y", 11)
                    .attr("fill", "#0c0d10")
                    .attr("font-size", 10)
                    .attr("font-weight", 600)
                    .attr("pointer-events", "none")
                    .style("user-select", "none")
                    .text((d) =>
                        d.x1 - d.x0 > 30 && d.y1 - d.y0 > 12 ? d.data.name : "",
                    );

                cell.on("mouseenter", function (_e, d) {
                    onHover(d);
                    d3.select(this)
                        .select("rect")
                        .attr("fill-opacity", 1)
                        .attr("stroke", "#fff8ee")
                        .attr("stroke-width", 1.25);
                })
                    .on("mouseleave", function (_e, d) {
                        onHover(null);
                        d3.select(this)
                            .select("rect")
                            .attr("fill-opacity", d.children ? 0.85 : 0.7)
                            .attr("stroke", "#070809")
                            .attr("stroke-width", 0.75);
                    })
                    .on("click", (_e, d) => {
                        if (d.children) zoom(d);
                    });
            }

            function zoom(p: SBNode) {
                focus = p;
                onFocus(p);
                render(p);
            }

            zoomRef.current = zoom;
            onFocus(root);
            onHover(null);
            zoom(focus);

            const ro = new ResizeObserver(() => render(focus));
            ro.observe(host);

            return () => {
                ro.disconnect();
                host.replaceChildren();
            };
        }, [data, onHover, onFocus]);

        return <div className="treemap" ref={hostRef} />;
    },
);
