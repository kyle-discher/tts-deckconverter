package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.uber.org/zap"

	dc "deckconverter"
	"deckconverter/plugins"
	"deckconverter/tts"
)

type options map[string]string

func (o *options) String() string {
	options := make([]string, 0, len(*o))

	for k, v := range *o {
		options = append(options, k+"="+v)
	}

	return strings.Join(options, ",")
}

func (o *options) Set(value string) error {
	kv := strings.Split(value, "=")

	if len(kv) != 2 {
		return errors.New("invalid option value: " + value)
	}

	k := kv[0]
	v := kv[1]

	(*o)[k] = v

	return nil
}

func getAvailableOptions(pluginNames []string) string {
	var sb strings.Builder

	for _, pluginName := range pluginNames {
		plugin, found := dc.Plugins[pluginName]
		if !found {
			fmt.Fprintf(os.Stderr, "Invalid mode: %s\n", pluginName)
			flag.Usage()
			os.Exit(1)
		}

		sb.WriteString("\n")
		sb.WriteString(pluginName)
		sb.WriteString(":")

		options := plugin.AvailableOptions()

		if len(options) == 0 {
			sb.WriteString(" no option available")
			continue
		}

		optionKeys := make([]string, 0, len(options))
		for key := range options {
			optionKeys = append(optionKeys, key)
		}
		sort.Strings(optionKeys)

		for _, key := range optionKeys {
			option := options[key]

			sb.WriteString("\n")
			sb.WriteString("\t")
			sb.WriteString(key)
			sb.WriteString(" (")
			sb.WriteString(option.Type.String())
			sb.WriteString("): ")
			sb.WriteString(option.Description)

			if option.DefaultValue != nil {
				sb.WriteString(" (default: ")
				sb.WriteString(fmt.Sprintf("%v", option.DefaultValue))
				sb.WriteString(")")
			}
		}
	}

	return sb.String()
}

func getAvailableBacks(pluginNames []string) string {
	var sb strings.Builder

	for _, pluginName := range pluginNames {
		plugin, found := dc.Plugins[pluginName]
		if !found {
			fmt.Fprintf(os.Stderr, "Invalid mode: %s\n", pluginName)
			flag.Usage()
			os.Exit(1)
		}

		sb.WriteString("\n")
		sb.WriteString(pluginName)
		sb.WriteString(":")

		backs := plugin.AvailableBacks()

		if len(backs) == 0 {
			sb.WriteString(" no card back available")
			continue
		}

		backKeys := make([]string, 0, len(backs))
		for key := range backs {
			if key != plugins.DefaultBackKey {
				backKeys = append(backKeys, key)
			}
		}
		sort.Strings(backKeys)

		// Make sure "default" is first
		if _, found := backs[plugins.DefaultBackKey]; found {
			backKeys = append([]string{plugins.DefaultBackKey}, backKeys...)
		}

		for _, key := range backKeys {
			back := backs[key]

			sb.WriteString("\n")
			sb.WriteString("\t")
			sb.WriteString(key)
			sb.WriteString(": ")
			sb.WriteString(back.Description)
		}
	}

	return sb.String()
}

func handleTarget(target, mode, outputFolder, backURL string, templateMode bool, options options, log *zap.SugaredLogger) error {
	log.Infof("Processing %s", target)

	decks, err := dc.Parse(target, mode, options, log)
	if err != nil {
		return err
	}

	if templateMode {
		err := tts.GenerateTemplates([][]*plugins.Deck{decks}, outputFolder, log)
		if err != nil {
			return err
		}
	}

	tts.Generate(decks, backURL, outputFolder, log)

	return nil
}

func main() {
	var (
		logger       *zap.Logger
		backURL      string
		back         string
		debug        bool
		mode         string
		outputFolder string
		templateMode bool
	)

	availableModes := dc.AvailablePlugins()
	availableOptions := getAvailableOptions(availableModes)
	availableBacks := getAvailableBacks(availableModes)

	options := make(options)

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s TARGET\n\nFlags:\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}

	flag.StringVar(&backURL, "backURL", "", "custom URL for the card backs (cannot be used with \"-back\")")
	flag.BoolVar(&debug, "debug", false, "enable debug logging")
	flag.StringVar(&mode, "mode", "", "available modes: "+strings.Join(availableModes, ", "))
	flag.StringVar(&outputFolder, "output", "", "destination folder (defaults to the current folder)")
	flag.BoolVar(&templateMode, "template", false, "download each images and create a deck template instead of referring to each image individually")
	flag.Var(&options, "option", "plugin specific option (can have multiple)"+availableOptions)
	flag.StringVar(&back, "back", "", "card back (cannot be used with \"-backURL\"):"+availableBacks)

	flag.Parse()

	if flag.NArg() == 0 || flag.NArg() > 1 {
		fmt.Fprintf(os.Stderr, "A target is required\n\n")
		flag.Usage()
		os.Exit(1)
	}

	plugin, found := dc.Plugins[mode]
	if len(mode) > 0 && !found {
		fmt.Fprintf(os.Stderr, "Invalid mode: %s\n\n", mode)
		flag.Usage()
		os.Exit(1)
	}

	if len(outputFolder) > 0 {
		if stat, err := os.Stat(outputFolder); err != nil || !stat.IsDir() {
			fmt.Fprintf(os.Stderr, "Output folder %s doesn't exist or is not a directory\n\n", outputFolder)
			flag.Usage()
			os.Exit(1)
		}
	} else {
		var err error
		// Set the output directory to the current working directory
		outputFolder, err = os.Getwd()
		if err != nil {
			log.Fatal(err)
		}
	}

	if len(back) > 0 && len(backURL) > 0 {
		fmt.Fprintf(os.Stderr, "\"-back\" and \"-backURL\" cannot be used at the same time\n\n")
		flag.Usage()
		os.Exit(1)
	}

	if len(back) > 0 && plugin == nil {
		fmt.Fprintf(os.Stderr, "You need to choose a mode in order to use \"-back\"\n\n")
		flag.Usage()
		os.Exit(1)
	}

	if len(back) > 0 {
		chosenBack, found := plugin.AvailableBacks()[back]
		if !found {
			fmt.Fprintf(os.Stderr, "Invalid back for %s: %s\n\n", mode, back)
			flag.Usage()
			os.Exit(1)
		}
		backURL = chosenBack.URL
	}

	target := flag.Args()[0]

	if debug {
		logger, _ = zap.NewDevelopment()
		defer logger.Sync()
	} else {
		config := zap.NewProductionConfig()
		config.Encoding = "console"
		logger, _ = config.Build()
		defer logger.Sync()
	}

	log := logger.Sugar()

	if info, err := os.Stat(target); err == nil && info.IsDir() {
		log.Infof("Processing directory %s", target)

		files := []string{}

		err = filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if path == target {
				// The WalkFun is first called with the folder itself as argument
				// Skip it
				return nil
			}

			if info.IsDir() {
				log.Infof("Ignoring directory %s", path)
				// Do not process the files in the subfolder
				return filepath.SkipDir
			}

			// Do not process the file inside the WalkFun, overwise if we
			// generate files inside the target directory, these generated
			// files will be picked up by filepath.Walk
			files = append(files, path)

			return nil
		})
		if err != nil {
			log.Fatal(err)
		}

		for _, file := range files {
			err = handleTarget(file, mode, outputFolder, backURL, templateMode, options, log)
			if err != nil {
				log.Fatal(err)
			}
		}

		return
	}

	err := handleTarget(target, mode, outputFolder, backURL, templateMode, options, log)
	if err != nil {
		log.Fatal(err)
	}
}
