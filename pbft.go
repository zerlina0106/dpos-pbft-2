package main

import (
	"fmt"
	"log"
	"math"
	"strconv"
	"sync"
)

var (
	maxFPNode = int64(math.Floor(float64((numberOfDelegates - 1) / 3)))
)

const (
	PBFTStateNone = iota
	PBFTStatePrepare
	PBFTStateCommit
)

type ConsensusInfo struct {
	Height      int64
	Hash        string
	VotesNumber int64
	Votes       map[string]struct{}
}

type Pbft struct {
	Mutex            sync.RWMutex
	State            int64
	Node             *Node
	PendingBlocks    map[string]*Block
	CurrentSlot      int64
	PrepareInfo      *ConsensusInfo
	CommitInfos      map[string]*ConsensusInfo
	PrepareHashCache map[string]struct{}
	CommitHashCache  map[string]struct{}
	Chain            *Blockchain
}

func NewConsensusInfo() *ConsensusInfo {
	return &ConsensusInfo{
		VotesNumber: 1,
		Votes:       make(map[string]struct{}, 0),
	}
}

func NewPbft(node *Node) *Pbft {
	pbft := &Pbft{
		State:            PBFTStateNone,
		Node:             node,
		PendingBlocks:    make(map[string]*Block, 0),
		CurrentSlot:      0,
		PrepareInfo:      NewConsensusInfo(),
		CommitInfos:      make(map[string]*ConsensusInfo, 0),
		PrepareHashCache: make(map[string]struct{}, 0),
		CommitHashCache:  make(map[string]struct{}, 0),
		Chain:            node.Chain,
	}

	return pbft
}

func (p *Pbft) AddBlock(block *Block, slotNumber int64) {
	hash := block.GetHash()
	p.Mutex.Lock()
	p.PendingBlocks[hash] = block
	p.Mutex.Unlock()

	if slotNumber > p.CurrentSlot {
		p.ClearState()
	}

	if p.State == PBFTStateNone {
		p.CurrentSlot = slotNumber
		p.State = PBFTStatePrepare
		p.PrepareInfo.Height = block.GetHeight()
		p.PrepareInfo.Hash = block.GetHash()
		p.PrepareInfo.VotesNumber = 1
		p.Mutex.Lock()
		p.PrepareInfo.Votes[strconv.FormatInt(p.Node.Id, 10)] = struct{}{}
		p.Mutex.Unlock()

		stageMsg := StageMessage{
			Height: block.GetHeight(),
			Hash:   block.GetHash(),
			Signer: strconv.FormatInt(p.Node.Id, 10),
		}

		p.Node.Broadcast(PrepareMessage(p.Node.Id, stageMsg))
	}
}

//func (p *Pbft) CommitBlock(hash string) {
//	fmt.Println("[Commit Block]", hash)
//	block := p.PendingBlocks[hash]
//}

func (p *Pbft) ClearState() {
	p.State = PBFTStateNone
	p.PrepareInfo = NewConsensusInfo()
	p.Mutex.Lock()
	p.CommitInfos = make(map[string]*ConsensusInfo)
	p.PendingBlocks = make(map[string]*Block)
	p.Mutex.Unlock()

	//	for k, _ := range p.CommitInfos {
	//		delete(p.CommitInfos, k)
	//	}
	//
	//	for k, _ := range p.PendingBlocks {
	//		delete(p.PendingBlocks, k)
	//	}
}

func (p *Pbft) handlePrepareMessage(msg *Message) {
	//fmt.Printf("NodeId %d receive prepare message: %s\n", p.Node.Id, msg.Body.(StageMessage).Hash)
	stageMsg := msg.Body.(StageMessage)
	cacheKey := fmt.Sprintf("%s:%d:%s", stageMsg.Hash, stageMsg.Height, stageMsg.Signer)

	p.Mutex.RLock()
	_, ok := p.PrepareHashCache[cacheKey]
	p.Mutex.RUnlock()

	if !ok {
		p.Mutex.Lock()
		p.PrepareHashCache[cacheKey] = struct{}{}
		p.Mutex.Unlock()
		p.Node.Broadcast(msg)
	} else {
		return
	}

	_, voted := p.PrepareInfo.Votes[stageMsg.Signer]

	if p.State == PBFTStatePrepare && stageMsg.Hash == p.PrepareInfo.Hash &&
		stageMsg.Height == p.PrepareInfo.Height && !voted {
		p.Mutex.Lock()
		p.PrepareInfo.Votes[stageMsg.Signer] = struct{}{}
		p.Mutex.Unlock()
		p.PrepareInfo.VotesNumber++

		if p.PrepareInfo.VotesNumber > maxFPNode {
			p.State = PBFTStateCommit
			commitInfo := NewConsensusInfo()
			commitInfo.Hash = p.PrepareInfo.Hash
			commitInfo.Height = p.PrepareInfo.Height
			commitInfo.Votes[strconv.FormatInt(p.Node.Id, 10)] = struct{}{}
			p.Mutex.Lock()
			p.CommitInfos[commitInfo.Hash] = commitInfo
			p.Mutex.Unlock()

			stageMsg := StageMessage{
				Height: p.PrepareInfo.Height,
				Hash:   p.PrepareInfo.Hash,
				Signer: strconv.FormatInt(p.Node.Id, 10),
			}

			p.Node.Broadcast(CommitMessage(p.Node.Id, stageMsg))
		}
	}
}

func (p *Pbft) handleCommitMessage(msg *Message) {
	//fmt.Println("[Commit Message]", msg.Body)
	stageMsg := msg.Body.(StageMessage)
	cacheKey := fmt.Sprintf("%s:%d:%s", stageMsg.Hash, stageMsg.Height, stageMsg.Signer)

	p.Mutex.RLock()
	_, ok := p.CommitHashCache[cacheKey]
	p.Mutex.RUnlock()

	if !ok {
		p.Mutex.Lock()
		p.CommitHashCache[cacheKey] = struct{}{}
		p.Mutex.Unlock()
		p.Node.Broadcast(msg)
	} else {
		return
	}

	p.Mutex.RLock()
	commitInfo := p.CommitInfos[stageMsg.Hash]
	p.Mutex.RUnlock()

	if commitInfo != nil {
		if _, ok := commitInfo.Votes[stageMsg.Signer]; !ok {
			p.Mutex.Lock()
			commitInfo.Votes[stageMsg.Signer] = struct{}{}
			p.Mutex.Unlock()
			commitInfo.VotesNumber++
			//fmt.Println("Number of Votes for Commit", commitInfo)
			if commitInfo.VotesNumber > 2*maxFPNode {
				p.Mutex.RLock()
				p.Chain.CommitBlock(p.PendingBlocks[stageMsg.Hash])
				p.Mutex.RUnlock()
				p.ClearState()
			}
		}
	} else {
		commitInfo := NewConsensusInfo()
		commitInfo.Hash = stageMsg.Hash
		commitInfo.Height = stageMsg.Height
		commitInfo.Votes[strconv.FormatInt(p.Node.Id, 10)] = struct{}{}
		p.Mutex.Lock()
		p.CommitInfos[stageMsg.Hash] = commitInfo
		p.Mutex.Unlock()
	}
}

func (p *Pbft) ProcessStageMessage(msg *Message) {
	switch msg.Type {
	case MessageTypePrepare:
		p.handlePrepareMessage(msg)
	case MessageTypeCommit:
		p.handleCommitMessage(msg)
	default:
		log.Println("ProcessStageMessage cannot find match message type")
	}
}
