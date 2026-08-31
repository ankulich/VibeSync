/**
 * STUB — placeholder for the upcoming own-video upload flow.
 *
 * The architecture is reserved for it: MediaSource.UPLOAD already exists in
 * the media proto, and the plan (ADR-0016) is uploads through the Storage
 * Service into MinIO with FFmpeg transcoding to HLS renditions so every
 * viewer can pick their own quality, with subtitle tracks alongside. Until
 * that ships, this panel only communicates that the option is coming.
 */
export default function UploadPanel() {
  return (
    <div className="flex flex-col gap-3 p-4">
      <p className="text-xs font-semibold uppercase tracking-wide text-gray-400">Own video</p>
      <div className="flex flex-col items-center gap-2 rounded-xl border border-dashed border-gray-700 bg-surface-overlay/40 px-4 py-12 text-center">
        <span className="text-2xl" aria-hidden="true">
          📹
        </span>
        <p className="text-sm text-gray-300">Upload your own video — coming soon</p>
        <p className="max-w-xs text-xs text-gray-500">
          Video files will be transcoded to multiple qualities with subtitles, watchable in sync
          just like YouTube. For now, add YouTube videos by link in the “Add media” tab.
        </p>
      </div>
    </div>
  );
}
