/**
 * Minimal ambient typings for the YouTube IFrame Player API
 * (https://developers.google.com/youtube/iframe_api_reference), loaded at
 * runtime from https://www.youtube.com/iframe_api — see YouTubePlayer.tsx.
 * Only the surface VibeSync drives is declared.
 */

declare namespace YT {
  interface PlayerVars {
    controls?: 0 | 1;
    disablekb?: 0 | 1;
    modestbranding?: 0 | 1;
    rel?: 0 | 1;
    playsinline?: 0 | 1;
    origin?: string;
    [key: string]: unknown;
  }

  interface PlayerEvent {
    target: Player;
    data?: unknown;
  }

  interface StateChangeEvent {
    target: Player;
    data: number;
  }

  interface Events {
    onReady?: (event: PlayerEvent) => void;
    onStateChange?: (event: StateChangeEvent) => void;
    onError?: (event: PlayerEvent) => void;
  }

  interface PlayerOptions {
    videoId?: string;
    width?: string | number;
    height?: string | number;
    playerVars?: PlayerVars;
    events?: Events;
  }

  class Player {
    constructor(element: HTMLElement | string, options: PlayerOptions);
    playVideo(): void;
    pauseVideo(): void;
    seekTo(seconds: number, allowSeekAhead: boolean): void;
    getCurrentTime(): number;
    getDuration(): number;
    getPlayerState(): number;
    getPlaybackRate(): number;
    setPlaybackRate(rate: number): void;
    getAvailablePlaybackRates(): number[];
    loadVideoById(videoId: string): void;
    cueVideoById(videoId: string): void;
    setVolume(volume: number): void;
    unMute(): void;
    destroy(): void;
  }

  /** Numeric player states as documented by the IFrame API. */
  const PlayerState: {
    readonly UNSTARTED: -1;
    readonly ENDED: 0;
    readonly PLAYING: 1;
    readonly PAUSED: 2;
    readonly BUFFERING: 3;
    readonly CUED: 5;
  };
}

interface Window {
  YT?: typeof YT;
  onYouTubeIframeAPIReady?: () => void;
}
