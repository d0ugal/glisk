const UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

export function formatBytes(bytes: number): string {
    if (!bytes || bytes < 1) return "0 B";
    const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), UNITS.length - 1);
    const v = bytes / Math.pow(1024, i);
    const decimals = i === 0 ? 0 : v >= 100 ? 0 : v >= 10 ? 1 : 2;
    return `${v.toFixed(decimals)} ${UNITS[i]}`;
}

export function formatNumber(n: number): string {
    return n.toLocaleString();
}

export function formatPercent(part: number, whole: number): string {
    if (!whole) return "0%";
    const p = (part / whole) * 100;
    if (p < 0.1 && p > 0) return "<0.1%";
    return `${p.toFixed(p >= 10 ? 0 : 1)}%`;
}

export function formatRelative(unix: number): string {
    if (!unix) return "never";
    const diff = Date.now() / 1000 - unix;
    if (diff < 60) return "just now";
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
    return `${Math.floor(diff / 86400)}d ago`;
}

export function formatClock(unix: number): string {
    if (!unix) return "—";
    return new Date(unix * 1000).toLocaleString(undefined, {
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
    });
}
