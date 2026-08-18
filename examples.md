# API Examples

## Register App / Get API Key

`curl -s -X POST http://localhost:8085/api/register -H "Content-Type: application/json" -d '{"name": "Console2"}'`

## Request Permissions

`curl -s -X POST http://localhost:8085/api/request-permissions -H "X-Proxy-Api-Key: 064b58ede99457379b787bc90c060bb7670ac6d17d38d9f4d09f08756e7aea60" -H "Content-Type: application/json" -d '{"permissions": ["list_characters", "list_accounts"]}'`

## List Characters

`curl -s http://localhost:8085/api/characters -H "X-Proxy-Api-Key: 064b58ede99457379b787bc90c060bb7670ac6d17d38d9f4d09f08756e7aea60"`

## Request ESI Access

`curl -s -X POST http://localhost:8085/api/request-access -H "X-Proxy-Api-Key: 944de15c801df8f350ee6e7e143dc05f6629d0f073208bd8c27fb8498c556e18" -d '{"character_id": 1978535095, "scopes": ["esi-location.read_location.v1"]}'`

## Get Character Location

`curl -s http://localhost:8085/proxy/1978535095/characters/1978535095/location/ -H "X-Proxy-Api-Key: 944de15c801df8f350ee6e7e143dc05f6629d0f073208bd8c27fb8498c556e18"`
