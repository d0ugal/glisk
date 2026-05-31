export interface TreeNode {
    name: string;
    size: number;
    dir?: boolean;
    children?: TreeNode[];
}

export interface Status {
    scanning: boolean;
    hasData: boolean;
    files: number;
    dirs: number;
    totalBytes: number;
    lastScanUnix: number;
    durationSec: number;
    nextScanUnix: number;
    progress: number;
    root: string;
    error?: string;
}

export interface TreeResult {
    status: Status;
    tree: TreeNode | null;
}

export interface VolumeInfo {
    id: string;
    label: string;
    status: Status;
}
