// Package db provides bbolt-backed persistence for all game data.
// Each entity type lives in its own bucket; keys are big-endian fixed-width
// byte slices so that bbolt's cursor iteration returns records in numeric order.
package db

import "encoding/binary"

// Bucket names
var (
	BucketShips      = []byte("ships")
	BucketPlanets    = []byte("planets")
	BucketStars      = []byte("stars")
	BucketRaces      = []byte("races")
	BucketSData      = []byte("sdata")      // global StarData (single record)
	BucketPower      = []byte("power")      // power report (single record)
	BucketBlocks     = []byte("blocks")     // power blocks (single record)
	BucketCommodities = []byte("commodities")
	BucketShipFree   = []byte("ship_free")  // free ship number list
	BucketCommodFree = []byte("commod_free")
	BucketMeta       = []byte("meta")       // update_time, segment_time, counters
	BucketNews       = []byte("news")       // keyed by [type(1)|seq(8)]
	BucketTelegrams  = []byte("telegrams")  // keyed by [player(1)|seq(8)]
)

// Meta keys
var (
	KeyUpdateTime   = []byte("update_time")
	KeySegmentTime  = []byte("segment_time")
	KeyNumUpdates   = []byte("num_updates")
	KeyNumSegments  = []byte("num_segments")
	KeyGlobalLists  = []byte("global_lists") // ship_free / commod_free JSON arrays
)

// ShipKey encodes a ship number as a 4-byte big-endian key.
func ShipKey(num uint32) []byte {
	k := make([]byte, 4)
	binary.BigEndian.PutUint32(k, num)
	return k
}

// PlanetKey encodes (starNum, planetNum) as a 2-byte key.
func PlanetKey(starNum, planetNum uint8) []byte {
	return []byte{starNum, planetNum}
}

// StarKey encodes a star number as a 2-byte big-endian key.
func StarKey(num uint8) []byte {
	return []byte{0, num}
}

// RaceKey encodes a player number (1-indexed) as a 2-byte big-endian key.
func RaceKey(playerNum int) []byte {
	k := make([]byte, 2)
	binary.BigEndian.PutUint16(k, uint16(playerNum))
	return k
}

// CommodKey encodes a commodity number as a 4-byte big-endian key.
func CommodKey(num uint32) []byte {
	k := make([]byte, 4)
	binary.BigEndian.PutUint32(k, num)
	return k
}

// NewsKey encodes (newsType, sequence) as a 9-byte key.
func NewsKey(newsType uint8, seq uint64) []byte {
	k := make([]byte, 9)
	k[0] = newsType
	binary.BigEndian.PutUint64(k[1:], seq)
	return k
}

// TelegramKey encodes (playerNum, sequence) as a 9-byte key.
func TelegramKey(playerNum uint8, seq uint64) []byte {
	k := make([]byte, 9)
	k[0] = playerNum
	binary.BigEndian.PutUint64(k[1:], seq)
	return k
}

// TelegramKeyPG encodes (playerNum, governor, sequence) as a 10-byte key,
// so telegrams for each player/governor pair are stored contiguously.
func TelegramKeyPG(playerNum, governor uint8, seq uint64) []byte {
	k := make([]byte, 10)
	k[0] = playerNum
	k[1] = governor
	binary.BigEndian.PutUint64(k[2:], seq)
	return k
}

// TelegramPrefix returns the 2-byte prefix for a (playerNum, governor) pair,
// used with bbolt Cursor.Seek to iterate only that pair's telegrams.
func TelegramPrefix(playerNum, governor uint8) []byte {
	return []byte{playerNum, governor}
}
