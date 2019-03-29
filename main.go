package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"log"
	"math"
	"path/filepath"
	"strings"
	//"github.com/nfnt/resize"
	"io/ioutil"
	"os"
	"strconv"

	"github.com/nf/cr2"
	"github.com/xor-gate/goexif2/exif"
	"github.com/xor-gate/goexif2/mknote"
	"github.com/xor-gate/goexif2/tiff"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
)

type CmdLineOpts struct {
	ScanDir             string
	ReadDataFrom        string
	SaveDataTo          string
	SaveDataOverwriteOK bool
	Verbose             bool
}

func ParseCommandLineOpts() (CmdLineOpts, error) {
	var opt CmdLineOpts
	flag.StringVar(&opt.ScanDir, "scan-dir", ".", "Scan .cr2 files in this directory for image stats")
	flag.StringVar(&opt.SaveDataTo, "save-data-to", "", "After scaning CR2 files save image stats into this JSON file")
	flag.StringVar(&opt.ReadDataFrom, "read-data-from", "", "read JSON file for data instead of scaning .cr2 files")
	flag.BoolVar(&opt.SaveDataOverwriteOK, "data-overwrite", false, "If save-data-to file exist, overwrite it")
	flag.BoolVar(&opt.Verbose, "v", false, "print more info while processing")
	help := flag.Bool("h", false, "print usage and exit")
	flag.Parse()
	if *help {
		flag.Usage()
		os.Exit(0)
	}
	if opt.ScanDir == "" && opt.ReadDataFrom == "" {
		return opt, fmt.Errorf("scan-dir or read-data-from should not be both empty")
	}
	if opt.ReadDataFrom != "" && opt.ReadDataFrom == opt.SaveDataTo {
		return opt, fmt.Errorf("read-data-from and save-data-to should not be same file")
	}
	return opt, nil
}

type ImageStat struct {
	FileName    string
	Brightness  uint64
	XResolution uint64
	YResolution uint64
	Exposure    string
	ExposureF64 float64
	Iris        string // Aperture
	IrisF64     float64
	ISO         int32
	CameraModel string
}

func (IS ImageStat) LogString() string {
	return fmt.Sprintf("CR2 image %s Exposure: %v(%v) ISO: %v Iris: %v/%v %vx%v %.03f Megapixel Brightness: %v",
		IS.FileName,
		IS.Exposure,
		IS.ExposureF64,
		IS.ISO,
		IS.IrisF64,
		IS.Iris,
		IS.XResolution,
		IS.YResolution,
		1e-6*float64(IS.XResolution*IS.YResolution),
		IS.Brightness,
	)
}

func ReadImageStat(path string) (IS ImageStat, err error) {
	IS.FileName = filepath.Base(path)
	img, meta, err := readCR2ImageFromDisk(path)
	if err != nil {
		return
	}
	IS.XResolution, IS.YResolution = uint64(img.Bounds().Dx()), uint64(img.Bounds().Dy())

	IS.Brightness = imageBrightness(img)

	if err = IS.parseISO(meta); err != nil {
		return
	}
	if err = IS.parseIris(meta); err != nil {
		return
	}
	if err = IS.parseExposure(meta); err != nil {
		return
	}
	return
}

func (IS *ImageStat) parseISO(meta *exif.Exif) (err error) {
	tag, err := meta.Get(exif.ISOSpeedRatings)
	if err != nil {
		return
	}
	intVal, err := tag.Int(0)
	if err != nil {
		return
	}
	IS.ISO = int32(intVal)
	return
}

func (IS *ImageStat) parseIris(meta *exif.Exif) (err error) {
	tag, err := meta.Get(exif.FNumber)
	if err != nil {
		return
	}
	_, IS.Iris, IS.IrisF64, err = rationalTagVals(tag)
	return
}

func (IS *ImageStat) parseExposure(meta *exif.Exif) (err error) {
	tag, err := meta.Get(exif.ExposureTime)
	if err != nil {
		return
	}
	IS.Exposure, _, IS.ExposureF64, err = rationalTagVals(tag)
	return
}

func rationalTagVals(tag *tiff.Tag) (str, strFloat string, val float64, err error) {
	str = tag.String()
	rationalValue, err := tag.Rat(0) // Index
	if err != nil {
		return
	}
	val, _ = rationalValue.Float64()
	strFloat = strconv.FormatFloat(val, 'g', 2, 64)
	return
}

func readCR2ImageFromDisk(path string) (img image.Image, meta *exif.Exif, err error) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	img, err = cr2.Decode(file)
	if err != nil {
		return
	}

	meta, err = exif.Decode(file)
	if err != nil {
		return
	}
	//meta.Walk(Printer{})

	return
}

func imageBrightness(img image.Image) (brightness uint64) {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			brightness += (uint64(r) + uint64(g) + uint64(b)) / 3
		}
	}
	return
}

//type Printer struct{}
//
//func (p Printer) Walk(name exif.FieldName, tag *tiff.Tag) error {
//	log.Printf("%v: %v", name, tag)
//	return nil
//}

func ensureScanDirOKorDie(scanDir string) {
	fi, err := os.Stat(scanDir)
	if err != nil {
		log.Fatal(err)
	}
	if !fi.Mode().IsDir() {
		log.Fatalf("Path \"%s\"should be directory", scanDir)
	}
}

func imageStatsFromDir(scanDir string, opt CmdLineOpts) (imageStats []ImageStat, err error) {
	if opt.Verbose {
		log.Printf("Scan directory %s for .cr2 files and image stats", opt.ScanDir)
	}
	files, err := ioutil.ReadDir(scanDir)
	if err != nil {
		return
	}
	imageStats = make([]ImageStat, 0, len(files))
	for _, file := range files {
		name := file.Name()
		if file.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(name), ".cr2") {
			continue
		}
		fullName := filepath.Join(scanDir, file.Name())
		IS, err := ReadImageStat(fullName)
		if err != nil {
			return nil, fmt.Errorf("file %s: %v", fullName, err)
		}

		if opt.Verbose {
			log.Printf("%s", IS.LogString())
		}
		imageStats = append(imageStats, IS)
	}
	if opt.Verbose {
		log.Printf("Scan directory %s complete", opt.ScanDir)
	}
	return
}

func LoadImageStats(opt CmdLineOpts) (imageStats []ImageStat, err error) {
	if opt.ReadDataFrom != "" {
		if opt.Verbose {
			log.Printf("Reading JSON data from file %s", opt.ReadDataFrom)
		}
		jsonText, err := ioutil.ReadFile(opt.ReadDataFrom)
		if err != nil {
			return nil, fmt.Errorf("read data from file %s error: %s", opt.ReadDataFrom, err)
		}
		err = json.Unmarshal(jsonText, &imageStats)
		if err != nil {
			return nil, fmt.Errorf("parse JSON data from file %s error: %s", opt.ReadDataFrom, err)
		}
	} else {
		ensureScanDirOKorDie(opt.ScanDir)
		imageStats, err = imageStatsFromDir(opt.ScanDir, opt)
		if err != nil {
			return nil, fmt.Errorf("scan directory %s error: %s", opt.ScanDir, err)
		}
	}
	return imageStats, nil
}

func SaveImageStats(imageStats []ImageStat, opt CmdLineOpts) error {
	if opt.SaveDataTo != "" {
		if opt.Verbose {
			log.Printf("Saving JSON data to file %s", opt.SaveDataTo)
		}
		if !opt.SaveDataOverwriteOK {
			if _, err := os.Stat(opt.SaveDataTo); !os.IsNotExist(err) {
				return fmt.Errorf("no overwrite allowed and file exists: %s", opt.SaveDataTo)
			}
		}
		jsonText, err := json.Marshal(imageStats)
		if err != nil {
			return fmt.Errorf("JSON marshal error for imageStats: %s", err)
		}
		//log.Printf("JSON: %s", jsonText)
		if err = ioutil.WriteFile(opt.SaveDataTo, jsonText, 0644); err != nil {
			return fmt.Errorf("Error writing JSON data to file %s: %s", opt.SaveDataTo, err)
		}
	}
	return nil
}

type IsoCurves map[string]plotter.XYs
type Curves map[int32]IsoCurves
type IsoBrights map[int32]uint64

func sortDataByCurves(imageStats []ImageStat) (Curves, IsoBrights) {
	curves := Curves{}

	maxBrightness := IsoBrights{}
	for _, IS := range imageStats {
		if curves[IS.ISO] == nil {
			curves[IS.ISO] = map[string]plotter.XYs{}
		}
		if curves[IS.ISO][IS.Iris] == nil {
			curves[IS.ISO][IS.Iris] = plotter.XYs{}
		}
		if maxBrightness[IS.ISO] < IS.Brightness {
			maxBrightness[IS.ISO] = IS.Brightness
		}
		curves[IS.ISO][IS.Iris] = append(curves[IS.ISO][IS.Iris],
			plotter.XY{
				X: IS.ExposureF64 / IS.IrisF64,
				Y: float64(IS.Brightness),
			})
		//log.Printf("ISO: %v Iris: %v Expo: %v Brightness: %v", IS.ISO, IS.IrisF64, IS.ExposureF64, IS.Brightness)
	}

	return curves, maxBrightness
}

func normalizeCurves(curves Curves, maxBrightness IsoBrights) Curves {
	for iso := range curves {
		for iris := range curves[iso] {
			for i, _ := range curves[iso][iris] {
				curves[iso][iris][i].X *= float64(maxBrightness[iso]) / 2
				curves[iso][iris][i].Y /= float64(maxBrightness[iso]) / 2
				curves[iso][iris][i].X = math.Log2(curves[iso][iris][i].X)
				curves[iso][iris][i].Y = math.Log2(curves[iso][iris][i].Y)
			}
		}
	}
	return curves
}

func organizeCurvesForPlot(curves Curves) []interface{} {
	namedCurves := []interface{}{}
	for iso := range curves {
		for iris := range curves[iso] {
			if len(curves[iso][iris]) < 3 {
				continue
			}
			title := fmt.Sprintf("ISO: %d Iris: %s", iso, iris)
			namedCurves = append(namedCurves, title)
			namedCurves = append(namedCurves, curves[iso][iris])
		}
	}
	return namedCurves
}

func main() {
	exif.RegisterParsers(mknote.All...)

	opt, err := ParseCommandLineOpts()
	if err != nil {
		log.Printf("error parse command line: %s", err)
		flag.Usage()
		os.Exit(1)
	}

	imageStats, err := LoadImageStats(opt)
	if err != nil {
		log.Fatalf("load data: %s", err)
	}
	if err = SaveImageStats(imageStats, opt); err != nil {
		log.Fatalf("save data: %s", err)
	}

	curves, maxBrightness := sortDataByCurves(imageStats)
	curves = normalizeCurves(curves, maxBrightness)
	namedCurvesForPlot := organizeCurvesForPlot(curves)

	p, err := plot.New()
	if err != nil {
		log.Fatal(err)
	}
	p.Title.Text = "Characteristic curve family"
	p.X.Label.Text = "Exposition"
	p.Y.Label.Text = "Brightness"

	err = plotutil.AddLinePoints(p, namedCurvesForPlot...)
	if err != nil {
		log.Fatal(err)
	}
	if err := p.Save(10*vg.Inch, 10*vg.Inch, "points.png"); err != nil {
		log.Fatal(err)
	}
	//log.Printf("%+v", imageStats)
}
