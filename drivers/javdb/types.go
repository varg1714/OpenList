package javdb

type Magnet struct {
	Tag       []string
	MagnetUrl string
	FileSize  uint64
}

type CreatActorReq struct {
	ActorName string `json:"actorName"`
	ActorId   string `json:"actorId"`
}
