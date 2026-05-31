import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
    fetchStatus,
    fetchTree,
    fetchVolumes,
    rescan,
    rescanFolder,
} from "./api";
import {
    formatBytes,
    formatClock,
    formatNumber,
    formatPercent,
    formatRelative,
} from "./format";
import { Sunburst, type SBNode, type SunburstHandle } from "./components/Sunburst";
import type { Status, TreeNode, VolumeInfo } from "./types";

// pathOf returns the folder names from the root down to (and including) a node,
// excluding the root label itself — i.e. the segments the API expects.
function pathOf(node: SBNode): string[] {
    return node
        .ancestors()
        .reverse()
        .slice(1)
        .map((a) => a.data.name);
}

export function App() {
    const [volumes, setVolumes] = useState<VolumeInfo[]>([]);
    const [vol, setVol] = useState<string>("");
    const [tree, setTree] = useState<TreeNode | null>(null);
    const [status, setStatus] = useState<Status | null>(null);
    const [hover, setHover] = useState<SBNode | null>(null);
    const [focus, setFocus] = useState<SBNode | null>(null);
    const [focusPath, setFocusPath] = useState<string[]>([]);
    const [error, setError] = useState<string | null>(null);
    const [folderBusy, setFolderBusy] = useState(false);
    const [folderMsg, setFolderMsg] = useState<string | null>(null);
    const sunRef = useRef<SunburstHandle>(null);

    const load = useCallback(async (volId: string) => {
        try {
            const r = await fetchTree(volId);
            setTree(r.tree);
            setStatus(r.status);
            setError(null);
        } catch (e) {
            setError(String(e));
        }
    }, []);

    // Discover volumes once, then load the first one.
    useEffect(() => {
        void (async () => {
            try {
                const vs = await fetchVolumes();
                setVolumes(vs);
                const first = vs[0]?.id ?? "";
                setVol(first);
                if (first) await load(first);
            } catch (e) {
                setError(String(e));
            }
        })();
    }, [load]);

    const switchVol = useCallback(
        (id: string) => {
            if (id === vol) return;
            setVol(id);
            setFocus(null);
            setFocusPath([]);
            setHover(null);
            setTree(null);
            setFolderMsg(null);
            void load(id);
        },
        [vol, load],
    );

    // While a full scan runs, poll status; refresh the tree once it finishes.
    const scanning = status?.scanning ?? false;
    useEffect(() => {
        if (!scanning || !vol) return;
        const id = window.setInterval(async () => {
            try {
                const st = await fetchStatus(vol);
                setStatus(st);
                if (!st.scanning) {
                    window.clearInterval(id);
                    void load(vol);
                }
            } catch {
                /* transient; keep polling */
            }
        }, 2000);
        return () => window.clearInterval(id);
    }, [scanning, vol, load]);

    const onScan = useCallback(async () => {
        if (!vol) return;
        try {
            await rescan(vol);
            const st = await fetchStatus(vol);
            setStatus(st);
        } catch (e) {
            setError(String(e));
        }
    }, [vol]);

    const onHover = useCallback((n: SBNode | null) => setHover(n), []);
    const onFocus = useCallback((n: SBNode) => {
        setFocus(n);
        setFocusPath(pathOf(n));
        setFolderMsg(null);
    }, []);

    // Rescan just the folder currently in view, then splice the fresh result
    // in. focusPath is preserved so the Sunburst restores this same folder.
    const onRescanFolder = useCallback(async () => {
        if (focusPath.length === 0) {
            void onScan();
            return;
        }
        setFolderBusy(true);
        setFolderMsg(null);
        try {
            const r = await rescanFolder(vol, focusPath);
            setTree(r.tree);
            setStatus(r.status);
            setFolderMsg(`updated ${focusPath.join(" / ")}`);
        } catch (e) {
            setFolderMsg(e instanceof Error ? e.message : String(e));
        } finally {
            setFolderBusy(false);
        }
    }, [vol, focusPath, onScan]);

    const total = tree?.size ?? 0;
    const detail = hover ?? focus;

    // Top 10 children of the folder in view, largest first.
    const topItems = useMemo<SBNode[]>(() => {
        const kids = (focus?.children ?? []) as SBNode[];
        return [...kids]
            .sort((a, b) => (b.value ?? 0) - (a.value ?? 0))
            .slice(0, 10);
    }, [focus]);

    const isFolder = !!focus?.children?.length;
    const atRoot = focusPath.length === 0;

    return (
        <div className="app">
            <div className="grain" aria-hidden />
            <header className="topbar">
                <div className="brand">
                    <h1>
                        gl<span>isk</span>
                    </h1>
                    <p>a glance at your disk</p>
                </div>

                {volumes.length > 1 && (
                    <div className="vol-switch" role="tablist">
                        {volumes.map((v) => (
                            <button
                                key={v.id}
                                role="tab"
                                aria-selected={v.id === vol}
                                className={v.id === vol ? "active" : ""}
                                onClick={() => switchVol(v.id)}
                            >
                                {v.label}
                            </button>
                        ))}
                    </div>
                )}

                <div className="meters">
                    <Meter label="mapped" value={formatBytes(total)} />
                    <Meter
                        label="files"
                        value={status ? formatNumber(status.files) : "—"}
                    />
                    <Meter
                        label="folders"
                        value={status ? formatNumber(status.dirs) : "—"}
                    />
                    <Meter
                        label="last scan"
                        value={status ? formatRelative(status.lastScanUnix) : "—"}
                        title={status ? formatClock(status.lastScanUnix) : ""}
                    />
                </div>

                <button className="scan-btn" onClick={onScan} disabled={scanning}>
                    {scanning ? (
                        <>
                            <span className="pulse" /> scanning
                        </>
                    ) : (
                        "scan now"
                    )}
                </button>
            </header>

            <main className="stage-wrap">
                <nav className="breadcrumb" aria-label="path">
                    {focus &&
                        focus
                            .ancestors()
                            .reverse()
                            .map((c, i, arr) => (
                                <span key={i} className="crumb">
                                    {i > 0 && <span className="sep">›</span>}
                                    <button
                                        onClick={() =>
                                            sunRef.current?.focusTo(c as SBNode)
                                        }
                                        disabled={i === arr.length - 1}
                                    >
                                        {c.data.name}
                                    </button>
                                </span>
                            ))}
                </nav>

                {error && <div className="banner error">{error}</div>}

                {!tree && !scanning && (
                    <EmptyState
                        nextScan={status?.nextScanUnix ?? 0}
                        onScan={onScan}
                    />
                )}

                {!tree && scanning && <Scanning progress={status?.progress ?? 0} />}

                {tree && (
                    <div className="stage">
                        <Sunburst
                            ref={sunRef}
                            data={tree}
                            onHover={onHover}
                            onFocus={onFocus}
                            focusPath={focusPath}
                        />
                        <div className="readout">
                            <span className="readout-name">
                                {detail ? detail.data.name : tree.name}
                            </span>
                            <span className="readout-size">
                                {formatBytes(detail ? (detail.value ?? 0) : total)}
                            </span>
                            <span className="readout-pct">
                                {formatPercent(
                                    detail ? (detail.value ?? 0) : total,
                                    total,
                                )}{" "}
                                of total
                            </span>
                        </div>
                    </div>
                )}
            </main>

            <aside className="inspector">
                {focus ? (
                    <>
                        <div className="insp-head">
                            <div className="insp-kind">
                                {isFolder
                                    ? `folder · ${formatNumber(focus.children?.length ?? 0)} items`
                                    : "file"}
                            </div>
                            <div className="insp-name">{focus.data.name}</div>
                            <div className="insp-size">
                                {formatBytes(focus.value ?? 0)}
                                <span className="insp-share">
                                    {formatPercent(focus.value ?? 0, total)} of
                                    total
                                </span>
                            </div>
                            {isFolder && (
                                <button
                                    className="rescan-folder"
                                    onClick={onRescanFolder}
                                    disabled={folderBusy}
                                    title={
                                        atRoot
                                            ? "Run a full rescan"
                                            : "Re-walk just this folder"
                                    }
                                >
                                    {folderBusy ? (
                                        <>
                                            <span className="pulse" /> rescanning
                                        </>
                                    ) : atRoot ? (
                                        "rescan all"
                                    ) : (
                                        "rescan this folder"
                                    )}
                                </button>
                            )}
                            {folderMsg && (
                                <div className="insp-msg">{folderMsg}</div>
                            )}
                        </div>

                        {isFolder && (
                            <div className="insp-list">
                                <div className="insp-list-title">
                                    largest items
                                </div>
                                {topItems.map((c, i) => {
                                    const share =
                                        (focus.value ?? 0) > 0
                                            ? ((c.value ?? 0) /
                                                  (focus.value ?? 1)) *
                                              100
                                            : 0;
                                    return (
                                        <button
                                            key={c.data.name + i}
                                            className="row"
                                            onMouseEnter={() => setHover(c)}
                                            onMouseLeave={() => setHover(null)}
                                            onClick={() =>
                                                c.children &&
                                                sunRef.current?.focusTo(c)
                                            }
                                        >
                                            <span className="row-rank">
                                                {i + 1}
                                            </span>
                                            <span className="row-main">
                                                <span className="row-name">
                                                    {c.children ? "" : "· "}
                                                    {c.data.name}
                                                </span>
                                                <span
                                                    className="row-bar"
                                                    style={{
                                                        width: `${share.toFixed(1)}%`,
                                                    }}
                                                />
                                            </span>
                                            <span className="row-size">
                                                {formatBytes(c.value ?? 0)}
                                            </span>
                                        </button>
                                    );
                                })}
                            </div>
                        )}

                        <div className="insp-path">
                            {focus
                                .ancestors()
                                .reverse()
                                .map((a) => a.data.name)
                                .join(" / ")}
                        </div>
                    </>
                ) : (
                    <div className="insp-hint">
                        <p>Hover a segment to inspect it.</p>
                        <p>Click to dive in · click the centre to climb out.</p>
                    </div>
                )}

                {status && (
                    <div className="insp-footer">
                        <span>
                            next nightly scan{" "}
                            <strong>{formatClock(status.nextScanUnix)}</strong>
                        </span>
                        {status.durationSec > 0 && (
                            <span>last took {status.durationSec.toFixed(1)}s</span>
                        )}
                    </div>
                )}
            </aside>
        </div>
    );
}

function Meter({
    label,
    value,
    title,
}: {
    label: string;
    value: string;
    title?: string;
}) {
    return (
        <div className="meter" title={title}>
            <span className="meter-value">{value}</span>
            <span className="meter-label">{label}</span>
        </div>
    );
}

function EmptyState({
    nextScan,
    onScan,
}: {
    nextScan: number;
    onScan: () => void;
}) {
    return (
        <div className="empty">
            <div className="empty-ring" aria-hidden />
            <h2>Nothing mapped yet</h2>
            <p>
                Scans run automatically overnight at a gentle priority. The next
                one is scheduled for <strong>{formatClock(nextScan)}</strong>.
            </p>
            <button className="scan-btn solo" onClick={onScan}>
                scan now
            </button>
        </div>
    );
}

function Scanning({ progress }: { progress: number }) {
    return (
        <div className="empty">
            <div className="empty-ring spin" aria-hidden />
            <h2>Mapping the volume…</h2>
            <p>
                Walking the filesystem at low priority.{" "}
                {progress > 0 && (
                    <>
                        <strong>{formatNumber(progress)}</strong> entries so far.
                    </>
                )}
            </p>
        </div>
    );
}
