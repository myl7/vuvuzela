// Copyright 2015 The Vuvuzela Authors. All rights reserved.
// Use of this source code is governed by the GNU AGPL
// license that can be found in the LICENSE file.

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/myl7/vuvuzela"
	"vuvuzela.io/alpenhorn"
	"vuvuzela.io/alpenhorn/config"
	"vuvuzela.io/alpenhorn/errors"
	"vuvuzela.io/alpenhorn/log"
)

var username = flag.String("username", "", "Alpenhorn username")
var debug = flag.Bool("debug", false, "Turn on debug mode")
var latency = flag.Duration("latency", 150*time.Millisecond, "latency to coordinator")
var home = flag.String("home", "", "The home directory in which Vuvuzela state is persisted")
var configServerURL = flag.String("url", "", "URL of config server")

func main() {
	flag.Parse()

	if *username == "" {
		fmt.Println("no username specified")
		os.Exit(1)
	}

	if *home == "" {
		u, err := user.Current()
		if err != nil {
			log.Fatal(err)
		}
		*home = u.HomeDir
	}
	confHome := filepath.Join(*home, ".vuvuzela")
	if err := os.MkdirAll(confHome, 0700); err != nil {
		log.Fatal(err)
	}

	var configClient *config.Client
	if *configServerURL == "" {
		configClient = config.StdClient
	} else {
		configClient = &config.Client{
			ConfigServerURL: *configServerURL,
		}
	}

	alpenhornClient, isNewAlpClient := LoadAlpenhornState(confHome, *username, configClient)
	vuvuzelaClient, isNewVuvuzelaClient := LoadVuvuzelaState(confHome, *username, configClient)
	vuvuzelaClient.CoordinatorLatency = *latency

	gc := &GuiClient{
		myName:          alpenhornClient.Username,
		convoClient:     vuvuzelaClient,
		alpenhornClient: alpenhornClient,
		pendingRounds:   make(map[uint32]pendingRound),
		active:          make(map[*Conversation]bool),
	}
	alpenhornClient.Handler = gc
	vuvuzelaClient.Handler = gc
	log.StdLogger.EntryHandler = gc

	gc.Run(launchStatus{
		isNewAlpenhornClient: isNewAlpClient,
		isNewVuvuzelaClient:  isNewVuvuzelaClient,
	})
}

func LoadAlpenhornState(confHome string, username string, configClient *config.Client) (client *alpenhorn.Client, new bool) {
	alpStatePath := filepath.Join(confHome, fmt.Sprintf("%s-alpenhorn-client-state", username))
	keywheelPath := filepath.Join(confHome, fmt.Sprintf("%s-keywheel", username))

	var err error
	client, err = alpenhorn.LoadClient(alpStatePath, keywheelPath)
	if os.IsNotExist(err) {
		client, err = generateAlpenhornClient(username, alpStatePath, keywheelPath, configClient)
		if err != nil {
			fmt.Printf("Failed to generate new Alpenhorn client: %s\n", err)
			os.Exit(1)
		}
		new = true
	}
	if err != nil {
		fmt.Printf("Failed to load alpenhorn client: %s\n", err)
		os.Exit(1)
	}

	client.ConfigClient = configClient
	return
}

func LoadVuvuzelaState(confHome string, username string, configClient *config.Client) (client *vuvuzela.Client, new bool) {
	vzStatePath := filepath.Join(confHome, fmt.Sprintf("%s-vuvuzela-client-state", username))

	var err error
	client, err = vuvuzela.LoadClient(vzStatePath)
	if os.IsNotExist(err) {
		client, err = generateVuvuzelaClient(vzStatePath, configClient)
		if err != nil {
			fmt.Printf("Failed to generate new Vuvuzela client: %s\n", err)
			os.Exit(1)
		}
		new = true
	}
	if err != nil {
		fmt.Printf("Failed to load vuvuzela client: %s\n", err)
		os.Exit(1)
	}

	client.ConfigClient = configClient
	return
}

func generateAlpenhornClient(username string, alpStatePath string, keywheelPath string, configClient *config.Client) (*alpenhorn.Client, error) {
	addFriendConfig, err := configClient.CurrentConfig("AddFriend")
	if err != nil {
		return nil, errors.Wrap(err, "fetching latest addfriend config")
	}
	dialingConfig, err := configClient.CurrentConfig("Dialing")
	if err != nil {
		return nil, errors.Wrap(err, "fetching latest dialing config")
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}

	client := &alpenhorn.Client{
		Username:           username,
		LongTermPrivateKey: privateKey,
		LongTermPublicKey:  publicKey,

		// For now, reuse the long term key for the PKG login key.
		PKGLoginKey: privateKey,

		ClientPersistPath:   alpStatePath,
		KeywheelPersistPath: keywheelPath,
	}
	err = client.Bootstrap(
		addFriendConfig,
		dialingConfig,
	)
	if err != nil {
		return nil, errors.Wrap(err, "bootstrapping alpenhorn client")
	}
	err = client.Persist()
	if err != nil {
		return nil, errors.Wrap(err, "persisting alpenhorn client")
	}
	return client, nil
}

func generateVuvuzelaClient(clientPath string, configClient *config.Client) (*vuvuzela.Client, error) {
	convoConfig, err := configClient.CurrentConfig("Convo")
	if err != nil {
		return nil, errors.Wrap(err, "fetching latest convo config")
	}

	client := &vuvuzela.Client{
		PersistPath: clientPath,
	}
	err = client.Bootstrap(convoConfig)
	if err != nil {
		return nil, errors.Wrap(err, "bootstrapping vuvuzela client")
	}
	err = client.Persist()
	if err != nil {
		return nil, errors.Wrap(err, "persisting vuvuzela client")
	}

	return client, nil
}
