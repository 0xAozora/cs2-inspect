package inspect

import (
	cs2 "cs2-inspect/cs2/protocol/protobuf"
	"cs2-inspect/types"
	"sort"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/0xAozora/go-steam/protocol/gamecoordinator"
	"google.golang.org/protobuf/proto"
)

type InspectTask struct {
	Infos       []*types.Info
	Resp        types.Response
	InventoryID uint64
	Remaining   uint32
	Ret         chan struct{}
}

// Inspect will put the request items into the inspect queue
// and a channel to notify when all its items is done
// If the queue is full, it will return the number of items that could be put into the queue
// resp is a slice of pointers to items, that will be filled with any successful inspect at the same index.
func (h *Handler) Inspect(req *types.Request, resp []*types.Info) (uint32, chan struct{}) {

	var c chan struct{} = nil

	h.InspectMutex.Lock()
	space := h.cap - h.len
	if space != 0 {

		c = make(chan struct{}, 1)

		inspect := InspectTask{
			Infos:       req.L,
			InventoryID: req.S,
			Ret:         c,
		}
		inspect.Resp.Info = resp

		l := uint32(len(req.L))
		if l <= space {
			space = l
		} else {
			inspect.Infos = req.L[:space]
		}
		atomic.AddUint32(&h.len, space)
		inspect.Remaining = space
		h.InspectMutex.Unlock() // Unlock here, so other inspect requests can be processed if channel blocks
		h.c <- &inspect
	} else {
		h.InspectMutex.Unlock()
	}

	return space, c
}

func (h *Handler) inspectLoop() {

	var index int
	var inspectTask *InspectTask
	var bot *Bot
	for {
		inspectTask = <-h.c

		for _, item := range inspectTask.Infos {

			var s, m uint64
			if inspectTask.InventoryID != 0 {
				s = inspectTask.InventoryID
			} else {
				s = item.S
				m = item.M
			}

			var fuse int
			// Try and find an ingame bot
			for {
				if fuse > len(h.botQueue) {
					h.log.Warn().Msg("No bots ingame")
					break
				}

				fuse++

				bot = h.botQueue[index]
				index++
				index %= len(h.botQueue)
				if bot == nil {
					continue
				}

				if bot.status != INGAME {
					continue
				}

				time.Sleep(time.Until(bot.lastInspect.Add(1100 * time.Millisecond)))
				// Check if the bot went offline while sleeping
				if bot.status != INGAME {
					continue
				}

				item.Time = time.Now()
				bot.lastInspect = item.Time

				// Map back to InspectTask
				h.ItemMutex.Lock()
				if _, ok := h.items[item.A]; ok {
					// Item already in inspect queue apparently
					// belonging to a different InspectTask if not a bug
					h.ItemMutex.Unlock()

					h.log.Debug().
						Str("bot", bot.Name).
						Uint64("itemID", item.A).
						Msgf("Item already being inspected")

					// Since we skip, there is one less item to inspect
					new := atomic.AddUint32(&inspectTask.Remaining, ^uint32(0))
					if new == 0 {
						inspectTask.Ret <- struct{}{}
					}
					break
				}
				h.items[item.A] = inspectTask
				h.ItemMutex.Unlock()

				if err := bot.Inspect(s, item.A, item.D, m); err != nil {

					conn := getTCPConn(bot.client)

					// Push to pool, we want to avoid stalling for the function
					h.Pool.Schedule(func() {
						h.handleError(bot, conn, 0, err)
					})

					// Clean up first
					h.ItemMutex.Lock()
					delete(h.items, item.A)
					h.ItemMutex.Unlock()

					// And finally go to the next bot
					continue
				}

				h.log.Debug().
					Str("bot", bot.Name).
					Uint64("itemID", item.A).
					Msgf("Inspecting Item")

				// Schedule removal of timeouted item
				h.timeTree.AddTask(&Task{
					T:     InspectTimeout,
					Value: item.A,
					Time:  item.Time.Add(2 * time.Second).UnixNano(),
				})

				break
			}

			// Decrement
			atomic.AddUint32(&h.len, ^uint32(0))
		}

		h.log.Debug().
			Msg("Inspect Loop Done")
	}

}

func (h *Handler) handleInspectResponse(bot *Bot, packet *gamecoordinator.GCPacket) {
	var res cs2.CMsgGCCStrike15V2_Client2GCEconPreviewDataBlockResponse
	err := proto.Unmarshal(packet.Body, &res)
	if err != nil {
		h.log.Err(err).
			Str("bot", bot.Name).
			Msg("Error Unmarshalling Inspect Response")
		return
	}
	if res.Iteminfo == nil {
		h.log.Err(err).
			Str("bot", bot.Name).
			Msg("Iteminfo is nil")
		return
	}

	now := time.Now()

	var float float32
	if res.Iteminfo.Paintwear != nil {
		float = *(*float32)(unsafe.Pointer(res.Iteminfo.Paintwear))
	}

	id := *res.Iteminfo.Itemid

	h.ItemMutex.Lock()
	inspect := h.items[id]
	if inspect == nil {
		// Inspect has Timeouted
		h.ItemMutex.Unlock()

		h.log.Debug().
			Str("bot", bot.Name).
			Uint64("itemID", id).
			Msgf("Inspect had timeouted")

		return
	}
	delete(h.items, id)
	h.ItemMutex.Unlock()

	h.log.Debug().
		Str("bot", bot.Name).
		Uint64("itemID", id).
		Float32("float", float).
		Msgf("Inspect response")

	// Find Index
	// Assuming Items are sorted by AssetID, which is the case with inventory items
	// Doesn't work with random items
	index := sort.Search(len(inspect.Infos), func(i int) bool {
		return id >= inspect.Infos[i].A
	})

	// Present safety check
	if index < len(inspect.Infos) && inspect.Infos[index].A == id {

		info := inspect.Infos[index]

		// Log Timing
		if h.metricsLogger != nil {
			h.metricsLogger.LogLookup(bot.Name, now.Sub(info.Time), &now, false)
		}

		// Add Info
		info.Float = float
		// Paint Seed 0 is nil
		if seed := res.Iteminfo.Paintseed; seed != nil {
			info.Seed = uint16(*res.Iteminfo.Paintseed)
		}

		// Stickers
		if l := len(res.Iteminfo.Stickers); l != 0 {
			info.Stickers = make([]types.Sticker, l)
			for i, sticker := range res.Iteminfo.Stickers {
				info.Stickers[i].ID = *sticker.StickerId
				if sticker.Wear != nil {
					info.Stickers[i].Wear = *sticker.Wear
				}
				if sticker.OffsetX != nil {
					info.Stickers[i].X = *sticker.OffsetX
				}
				if sticker.OffsetY != nil {
					info.Stickers[i].Y = *sticker.OffsetY
				}
			}
		}

		// Keychain
		if l := len(res.Iteminfo.Keychains); l != 0 {
			info.Keychain = &types.Keychain{
				ID:      *res.Iteminfo.Keychains[0].StickerId,
				Pattern: *res.Iteminfo.Keychains[0].Pattern,
				X:       *res.Iteminfo.Keychains[0].OffsetX,
				Y:       *res.Iteminfo.Keychains[0].OffsetY,
				Z:       *res.Iteminfo.Keychains[0].OffsetZ,
			}
		}

		// Add pointer
		inspect.Resp.Info[index] = info

	} else {
		h.log.Info().
			Str("bot", bot.Name).
			Uint64("itemID", id).
			Msg("Inspected Item not found")
	}

	// Decrement as we just finished one
	new := atomic.AddUint32(&inspect.Remaining, ^uint32(0))

	// Notify
	if new == 0 {
		inspect.Ret <- struct{}{}
	}
}
