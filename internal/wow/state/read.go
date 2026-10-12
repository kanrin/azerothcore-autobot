package state

// Read builds a Snapshot from the client's object cache.
//
// It never sends a packet and never blocks. A missing self object — not in the
// world yet, or a cache just cleared by a teleport — yields a zero Snapshot,
// which callers must treat as "unknown", not as "empty".
func Read(src Source) Snapshot {
	var snap Snapshot
	if src == nil {
		return snap
	}
	self := src.Object(src.CharGUID())
	if self == nil {
		return snap
	}

	snap.Money = self.Value(playerFieldCoinage)
	snap.Level = uint8(self.Level())

	snap.Quests = readQuestLog(self)

	// Equipment (0..18) and the equipped bags (19..22) share one GUID array.
	for slot := uint8(0); slot < equipmentSlots; slot++ {
		snap.addSlot(src, SlotRef{Container: ContainerEquipped, Slot: slot},
			self.GUIDField(playerFieldInvSlotHead+uint16(slot)*2))
	}
	for bag := uint8(0); bag < bagSlots; bag++ {
		snap.addSlot(src, SlotRef{Container: ContainerBag, Bag: bag},
			self.GUIDField(playerFieldInvSlotHead+uint16(equipmentSlots+bag)*2))
	}
	for slot := uint8(0); slot < backpackSlots; slot++ {
		snap.addSlot(src, SlotRef{Container: ContainerBackpack, Slot: slot},
			self.GUIDField(playerFieldPackSlot1+uint16(slot)*2))
	}
	for slot := uint8(0); slot < bankSlots; slot++ {
		snap.addSlot(src, SlotRef{Container: ContainerBank, Slot: slot},
			self.GUIDField(playerFieldBankSlot1+uint16(slot)*2))
	}
	for bag := uint8(0); bag < bankBagSlots; bag++ {
		snap.addSlot(src, SlotRef{Container: ContainerBankBag, Bag: bag},
			self.GUIDField(playerFieldBankBagSlot1+uint16(bag)*2))
	}

	// Bag contents live on the bag object, so they can only be walked once the
	// bag GUIDs are known.
	snap.Containers = append(snap.Containers, ContainerInfo{
		Slot:     SlotRef{Container: ContainerBackpack},
		Capacity: backpackSlots,
		Used:     snap.countIn(ContainerBackpack),
	})
	snap.Containers = append(snap.Containers, snap.walkBagContents(src)...)
	snap.Skills = readSkills(self)
	return snap
}

// addSlot records one slot. A zero GUID is an empty slot and adds nothing; a
// GUID whose object is not in the cache is recorded with Known=false.
func (snap *Snapshot) addSlot(src Source, ref SlotRef, guid uint64) {
	if guid == 0 {
		return
	}
	item := Item{Slot: ref, GUID: guid}
	if obj := src.Object(guid); obj != nil {
		item.Entry = obj.Entry
		item.Count = obj.Value(itemFieldStackCount)
		item.Durability = obj.Value(itemFieldDurability)
		item.MaxDurability = obj.Value(itemFieldMaxDurability)
		item.Flags = obj.Value(itemFieldFlags)
		item.Known = true
	}
	snap.Items = append(snap.Items, item)
}

// walkBagContents appends the items inside every used container and returns one
// ContainerInfo per equipped bag.
//
// The slot count comes from the bag's own CONTAINER_FIELD_NUM_SLOTS rather than
// from the field's 36-slot maximum: the server does not send slots a bag does
// not have, and reading them would invent empty ones.
func (snap *Snapshot) walkBagContents(src Source) []ContainerInfo {
	infos := make([]ContainerInfo, 0, bagSlots)
	for i := range snap.Items {
		item := &snap.Items[i]
		if item.Slot.Container != ContainerBag && item.Slot.Container != ContainerBankBag {
			continue
		}
		if !item.Known {
			infos = append(infos, ContainerInfo{Slot: item.Slot})
			continue
		}
		container := src.Object(item.GUID)
		if container == nil {
			item.Known = false
			infos = append(infos, ContainerInfo{Slot: item.Slot})
			continue
		}

		capacity := uint8(container.Value(containerFieldNumSlots))
		if capacity > maxContainerSlots {
			capacity = maxContainerSlots
		}
		used := uint8(0)
		for slot := uint8(0); slot < capacity; slot++ {
			guid := container.GUIDField(containerFieldSlot1 + uint16(slot)*2)
			if guid == 0 {
				continue
			}
			snap.addSlot(src, SlotRef{Container: item.Slot.Container, Bag: item.Slot.Bag, Slot: slot}, guid)
			used++
		}
		if item.Slot.Container == ContainerBag {
			infos = append(infos, ContainerInfo{Slot: item.Slot, Capacity: capacity, Used: used})
		}
	}
	return infos
}

// countIn counts the items directly inside one container (not in its bags).
func (snap *Snapshot) countIn(container Container) uint8 {
	var used uint8
	for _, item := range snap.Items {
		if item.Slot.Container == container {
			used++
		}
	}
	return used
}
