package policy

import (
	"encoding/binary"
	"sync"
	"time"

	"github.com/cespare/xxhash"
)

const (
	MASK_LEN                   = 24
	MIN_MASK_LEN               = 8
	MAX_MASK_LEN               = 32
	IF_TYPE_WAN                = 3
	NETMASK                    = 0xFFFFFFFF
	DEEPFLOW_POSITION_EXPORTER = 0x30000
	DATA_VALID_TIME            = 60 * time.Second
)

type IpNet struct {
	Ip       uint32
	Netmask  uint32
	SubnetId uint32
}

type PlatformData struct {
	Mac        uint64
	Ips        []*IpNet
	EpcId      int32
	DeviceType uint32
	DeviceId   uint32
	IfIndex    uint32
	IfType     uint32
	HostIp     uint32
	GroupIds   []uint32
}

type FastPlatformData struct {
	endpointInfo *EndpointInfo
	timestamp    time.Time
}

type IpMapDatas []map[IpKey]*PlatformData
type IpMapData map[IpKey]*PlatformData
type MacMapData map[MacKey]*PlatformData
type EpcIpMapData map[EpcIpKey]*PlatformData
type FastMapData map[MacIpInportKey]*FastPlatformData

type MacTable struct {
	macMap MacMapData
}

type IpTable struct {
	ipMap IpMapData
}

type EpcIpTable struct {
	epcIpMap EpcIpMapData
}

type FastTable struct {
	sync.RWMutex
	fastMap FastMapData
}

type CloudPlatformData struct {
	macTable      *MacTable
	ipTables      [MASK_LEN]*IpTable
	epcIpTable    *EpcIpTable
	ipGroup       *IpResourceGroup
	netmaskBitmap uint32
	fastTable     *FastTable
}

func NewCloudPlatformData() *CloudPlatformData {
	mactable := &MacTable{
		macMap: make(MacMapData),
	}
	var iptables [MASK_LEN]*IpTable
	for i := uint32(0); i < MASK_LEN; i++ {
		iptables[i] = &IpTable{
			ipMap: make(IpMapData),
		}
	}
	epciptable := &EpcIpTable{
		epcIpMap: make(EpcIpMapData),
	}
	fasttable := &FastTable{
		fastMap: make(FastMapData),
	}

	return &CloudPlatformData{
		macTable:      mactable,
		ipTables:      iptables,
		epcIpTable:    epciptable,
		ipGroup:       NewIpResourceGroup(),
		netmaskBitmap: uint32(0),
		fastTable:     fasttable,
	}
}

func (i *EndpointInfo) SetL2Data(data *PlatformData) {
	i.L2EpcId = data.EpcId
	i.L2DeviceType = data.DeviceType
	i.L2DeviceId = data.DeviceId
	i.HostIp = data.HostIp
	i.GroupIds = append(i.GroupIds, data.GroupIds...)
}

func (i *EndpointInfo) SetL3Data(data *PlatformData, ip uint32) {
	i.L3EpcId = -1
	if data.EpcId != 0 {
		i.L3EpcId = data.EpcId
	}
	i.L3DeviceType = data.DeviceType
	i.L3DeviceId = data.DeviceId

	for _, ipInfo := range data.Ips {
		if ipInfo.Ip == (ip & (NETMASK << (MAX_MASK_LEN - ipInfo.Netmask))) {
			i.SubnetId = ipInfo.SubnetId
			break
		}
	}
}

func (i *EndpointInfo) SetL3EndByTtl(data *PlatformData, ttl uint32) {
	if ttl == 64 || ttl == 128 || ttl == 255 {
		i.L3End = true
	}
}

func (i *EndpointInfo) SetL3EndByIp(data *PlatformData, ip uint32) {
	for _, ipInfo := range data.Ips {
		if ipInfo.Ip == (ip & (NETMASK << (MAX_MASK_LEN - ipInfo.Netmask))) {
			i.L3End = true
			break
		}
	}
}

func (i *EndpointInfo) SetL3EndByMac(data *PlatformData, mac uint64) {
	if data.Mac == mac {
		i.L3End = true
	}
}

func PortInDeepflowExporter(inPort uint32) bool {
	return DEEPFLOW_POSITION_EXPORTER == ((inPort) & DEEPFLOW_POSITION_EXPORTER)
}

func calcHashKey(mac uint64, ip uint32, inport uint32) uint64 {
	buf := make([]byte, 16)
	binary.BigEndian.PutUint64(buf, mac)
	binary.BigEndian.PutUint32(buf[8:], ip)
	binary.BigEndian.PutUint32(buf[12:], inport)
	return xxhash.Sum64(buf)
}

func (d *CloudPlatformData) GetDataByMac(key MacKey) *PlatformData {
	if info, ok := d.macTable.macMap[key]; ok {
		return info
	}

	return nil
}

func (d *CloudPlatformData) UpdateMacTable(macmap MacMapData) {
	if macmap != nil {
		d.macTable.macMap = macmap
	}
}

func (d *CloudPlatformData) GenerateMacData(vifdata []*PlatformData) MacMapData {
	macmap := make(MacMapData)
	for _, vifdata := range vifdata {
		if vifdata.Mac != 0 {
			macmap[MacKey(vifdata.Mac)] = vifdata
		}
	}
	return macmap
}

func IfHasNetmaskBit(bitmap uint32, k uint32) bool {
	return (bitmap & (1 << k)) != 0
}

func (d *CloudPlatformData) GetDataByIp(ip uint32) *PlatformData {
	for i := uint32(0); i < MASK_LEN; i++ {
		if !IfHasNetmaskBit(d.netmaskBitmap, i) {
			continue
		}
		subip := IpKey(ip & (NETMASK << i))
		if info, ok := d.ipTables[i].ipMap[subip]; ok {
			return info
		}
	}
	return nil
}

func (d *CloudPlatformData) GenerateIpData(vifdatas []*PlatformData) IpMapDatas {
	ips := make(IpMapDatas, MASK_LEN)

	for i := uint32(0); i < MASK_LEN; i++ {
		ips[i] = make(IpMapData)
	}
	for _, vifdata := range vifdatas {
		if vifdata.IfType != IF_TYPE_WAN {
			continue
		}
		for _, ipdata := range vifdata.Ips {
			netmask := MAX_MASK_LEN - ipdata.Netmask
			ips[netmask][IpKey(ipdata.Ip)] = vifdata
			d.netmaskBitmap |= 1 << netmask
		}
	}

	return ips
}

func (d *CloudPlatformData) UpdateIpTable(ipdatas IpMapDatas) {
	for index, ipmap := range ipdatas {
		d.ipTables[IpKey(index)].UpdateIpMap(ipmap)
	}
}

func (t *IpTable) UpdateIpMap(ipmap IpMapData) {
	if ipmap != nil {
		t.ipMap = ipmap
	}
}

func (d *CloudPlatformData) GetDataByEpcIp(epc int32, ip uint32) *PlatformData {
	key := EpcIpKey((uint64(epc) << 32) | uint64(ip))
	if info, ok := d.epcIpTable.epcIpMap[key]; ok {
		return info
	}

	return nil
}

func (d *CloudPlatformData) GenerateEpcIpData(vifdatas []*PlatformData) EpcIpMapData {
	epcipmap := make(EpcIpMapData)
	for _, vifdata := range vifdatas {
		for _, ipdata := range vifdata.Ips {
			key := EpcIpKey((uint64(vifdata.EpcId) << 32) | uint64(ipdata.Ip))
			epcipmap[key] = vifdata
		}
	}

	return epcipmap
}

func (d *CloudPlatformData) UpdateEpcIpTable(epcipmap EpcIpMapData) {
	if epcipmap != nil {
		d.epcIpTable.epcIpMap = epcipmap
	}
}

func (d *CloudPlatformData) InsertInfoToFastPath(mac uint64, ip uint32, inport uint32, data *FastPlatformData) {
	hash := MacIpInportKey(calcHashKey(mac, ip, inport))
	data.timestamp = time.Now()
	d.fastTable.Lock()
	d.fastTable.fastMap[hash] = data
	d.fastTable.Unlock()
}

func (d *CloudPlatformData) GetInfoByFastPath(mac uint64, ip uint32, inport uint32) *EndpointInfo {
	hash := MacIpInportKey(calcHashKey(mac, ip, inport))
	d.fastTable.RLock()
	if data, ok := d.fastTable.fastMap[hash]; ok {
		if DATA_VALID_TIME < time.Now().Sub(data.timestamp) {
			delete(d.fastTable.fastMap, hash)
			d.fastTable.RUnlock()
			return nil
		}
		d.fastTable.RUnlock()
		return data.endpointInfo
	}
	d.fastTable.RUnlock()
	return nil
}

func (d *CloudPlatformData) UpdateInterfaceTable(interfaces []*PlatformData) {
	if interfaces != nil {
		d.UpdateMacTable(d.GenerateMacData(interfaces))
		d.UpdateIpTable(d.GenerateIpData(interfaces))
		d.UpdateEpcIpTable(d.GenerateEpcIpData(interfaces))
	}
}

func (d *CloudPlatformData) GetEndpointInfo(mac uint64, ip uint32, inport uint32) *EndpointInfo {
	var data EndpointInfo
	if PortInDeepflowExporter(inport) {
		pfdata := d.GetDataByMac(MacKey(mac))
		if pfdata != nil {
			data.SetL2Data(pfdata)
			data.SetL3EndByIp(pfdata, ip)
			if pfdata = d.GetDataByEpcIp(data.L2EpcId, ip); pfdata == nil {
				pfdata = d.GetDataByIp(ip)
			}
			if pfdata != nil {
				data.SetL3Data(pfdata, ip)
			}
		} else {
			return nil
		}
	} else {
		pfdata := d.GetDataByIp(ip)
		if pfdata != nil {
			data.SetL3Data(pfdata, ip)
			data.SetL3EndByMac(pfdata, mac)
		} else {
			return nil
		}
	}
	fastdata := &FastPlatformData{
		endpointInfo: &data,
	}
	d.InsertInfoToFastPath(mac, ip, inport, fastdata)

	return &data
}

func (d *CloudPlatformData) GetEndpointData(key *LookupKey) *EndpointData {
	srcData := d.GetInfoByFastPath(key.SrcMac, key.SrcIp, key.RxInterface)
	if srcData == nil {
		srcData = d.GetEndpointInfo(key.SrcMac, key.SrcIp, key.RxInterface)
		d.ipGroup.Populate(key.SrcIp, srcData)
	}

	dstData := d.GetInfoByFastPath(key.DstMac, key.DstIp, key.RxInterface)
	if dstData == nil {
		dstData = d.GetEndpointInfo(key.DstMac, key.DstIp, key.RxInterface)
		d.ipGroup.Populate(key.DstIp, dstData)
	}

	if srcData != nil || dstData != nil {
		return &EndpointData{
			SrcInfo: srcData,
			DstInfo: dstData,
		}
	}

	return nil
}
