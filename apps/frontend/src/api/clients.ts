import { createClient } from '@connectrpc/connect';
import { AuthService } from '../gen/vibesync/auth/v1/auth_connect';
import { MediaService } from '../gen/vibesync/media/v1/media_connect';
import { ProviderService } from '../gen/vibesync/provider/v1/provider_connect';
import { RoomService } from '../gen/vibesync/room/v1/room_connect';
import { SyncService } from '../gen/vibesync/sync/v1/sync_connect';
import { UserService } from '../gen/vibesync/user/v1/user_connect';
import { transport } from './transport';

// Each client is created lazily on first use so importing this module stays
// side-effect light, and the same instance is reused afterwards.

function createAuthClient() {
  return createClient(AuthService, transport);
}

let authClient: ReturnType<typeof createAuthClient> | undefined;

/** Typed AuthService client (login, OAuth, refresh, logout, ...). */
export function getAuthClient(): ReturnType<typeof createAuthClient> {
  return (authClient ??= createAuthClient());
}

function createRoomClient() {
  return createClient(RoomService, transport);
}

let roomClient: ReturnType<typeof createRoomClient> | undefined;

/** Typed RoomService client (rooms + membership). */
export function getRoomClient(): ReturnType<typeof createRoomClient> {
  return (roomClient ??= createRoomClient());
}

function createSyncClient() {
  return createClient(SyncService, transport);
}

let syncClient: ReturnType<typeof createSyncClient> | undefined;

/** Typed SyncService client (subscribe stream, heartbeat, command, recover). */
export function getSyncClient(): ReturnType<typeof createSyncClient> {
  return (syncClient ??= createSyncClient());
}

function createMediaClient() {
  return createClient(MediaService, transport);
}

let mediaClient: ReturnType<typeof createMediaClient> | undefined;

/** Typed MediaService client (media catalog + room queue). */
export function getMediaClient(): ReturnType<typeof createMediaClient> {
  return (mediaClient ??= createMediaClient());
}

function createProviderClient() {
  return createClient(ProviderService, transport);
}

let providerClient: ReturnType<typeof createProviderClient> | undefined;

/** Typed ProviderService client (Spotify/YouTube search + resolve). */
export function getProviderClient(): ReturnType<typeof createProviderClient> {
  return (providerClient ??= createProviderClient());
}

function createUserClient() {
  return createClient(UserService, transport);
}

let userClient: ReturnType<typeof createUserClient> | undefined;

/** Typed UserService client (profile lookups). */
export function getUserClient(): ReturnType<typeof createUserClient> {
  return (userClient ??= createUserClient());
}
