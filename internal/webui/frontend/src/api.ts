import type { Status, TreeResult, VolumeInfo } from "./types";

function redirectIfUnauthed(res: Response) {
    if (res.status === 401) {
        window.location.href = "/login";
        throw new Error("unauthorized");
    }
}

async function getJSON<T>(url: string): Promise<T> {
    const res = await fetch(url);
    redirectIfUnauthed(res);
    if (!res.ok) throw new Error(`${url} → ${res.status}`);
    return res.json() as Promise<T>;
}

const q = (vol: string) => `?vol=${encodeURIComponent(vol)}`;

export function fetchVolumes(): Promise<VolumeInfo[]> {
    return getJSON<VolumeInfo[]>("/api/volumes");
}

export function fetchTree(vol: string): Promise<TreeResult> {
    return getJSON<TreeResult>(`/api/tree${q(vol)}`);
}

export function fetchStatus(vol: string): Promise<Status> {
    return getJSON<Status>(`/api/status${q(vol)}`);
}

export async function rescan(vol: string): Promise<boolean> {
    const res = await fetch(`/api/rescan${q(vol)}`, { method: "POST" });
    redirectIfUnauthed(res);
    if (!res.ok) throw new Error(`rescan → ${res.status}`);
    const body = (await res.json()) as { queued: boolean };
    return body.queued;
}

// rescanFolder re-walks a single folder (path segments below the root) and
// returns the updated tree. Throws with the server's message on failure.
export async function rescanFolder(
    vol: string,
    segments: string[],
): Promise<TreeResult> {
    const res = await fetch(`/api/rescan-folder${q(vol)}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ segments }),
    });
    redirectIfUnauthed(res);
    if (!res.ok) {
        let msg = `rescan-folder → ${res.status}`;
        try {
            const body = (await res.json()) as { error?: string };
            if (body.error) msg = body.error;
        } catch {
            /* ignore */
        }
        throw new Error(msg);
    }
    return res.json() as Promise<TreeResult>;
}
