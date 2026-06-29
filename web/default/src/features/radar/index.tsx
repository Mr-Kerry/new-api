export function CodexRadar() {
  return (
    <div className="h-[calc(100vh-4rem)] w-full overflow-hidden bg-white">
      <iframe
        src="https://codexradar.com/"
        title="Codex Radar"
        className="h-full w-full border-0"
        allow="clipboard-read; clipboard-write"
        referrerPolicy="strict-origin-when-cross-origin"
      />
    </div>
  )
}