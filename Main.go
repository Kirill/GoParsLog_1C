package main

import (
	"bufio"
	"encoding/gob"
	"encoding/xml"
	"flag"
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	. "GoParsLog_1C/Tools"
	"runtime/pprof"
)

//var Tools, error = build.Import("Tools/Chain", "", build.IgnoreVendor)

type Data struct {
	value  int64
	count  int
	OutStr string
}

type ImapData interface {
	MergeData(inData ImapData)
}

type IProfTime interface {
	Start() *ProfTime
	Stop()
}

type ProfTime struct {
	StartTime time.Time
}

type mapData map[string]*Data

const AddSizeChan = 10

var SortByCount, SortByValue, IO, v, cpuprofile, memprofile bool
var Top, Go int
var RootDir, Event, confPath string

func main() {
	defer new(ProfTime).Start().Stop()

	parsFlag()
	if cpuprofile {
		StartCPUProf()
		defer pprof.StopCPUProfile()
	}
	if memprofile {
		StartMemProf()
		defer pprof.StopCPUProfile()
	}

	if RootDir != "" {
		FindFiles(RootDir)
	} else if IO {
		readStdIn()
	} else {
		panic("Не определены входящие данные")
	}

}

func parsFlag() {
	flag.BoolVar(&SortByCount, "SortByCount", false, "Сортировка по количеству вызовов (bool)")
	flag.BoolVar(&SortByValue, "SortByValue", false, "Сортировка по значению (bool)")
	flag.BoolVar(&IO, "io", false, "Флаг указывающий, что данные будут поступать из StdIn (bool)")
	//flag.BoolVar(&v, "v", false, "Флаг включающий вывод лога. Не используется при чтении данных из потока StdIn (bool)")
	flag.IntVar(&Top, "Top", 100, "Ограничение на вывод по количеству записей")
	flag.IntVar(&Go, "Go", 10, "Количество воркеров которые будут обрабатывать файл")
	flag.StringVar(&RootDir, "RootDir", "", "Корневая директория")
	flag.BoolVar(&cpuprofile, "cpuprof", false, "Профилирование CPU (bool)")
	flag.BoolVar(&memprofile, "memprof", false, "Профилирование памяти (bool)")
	flag.StringVar(&confPath, "confPath", "", "Путь к конфигу")
	//flag.StringVar(&Event, "Event", "", "Событие ТЖ для группировки")

	flag.Parse()
}

func readConf() PatternList {
	if confPath == "" {
		return PatternList{}
	}
	if _, err := os.Stat(confPath); os.IsNotExist(err) {
		fmt.Printf("Конфигурационный файл %q не найден\n", confPath)
		return PatternList{}
	}

	file, err := ioutil.ReadFile(confPath)
	if err != nil {
		fmt.Printf("Ошибка открытия файла %q\n", confPath)
		return PatternList{}
	}

	result := PatternList{}
	// XML потому что в нем можно комментарии оставлять, в отличии от JSON
	if err = xml.Unmarshal(file, &result); err != nil {
		fmt.Printf("Ошибка десериализации файла %q\n %v", confPath, err.Error())
		return PatternList{}
	}
	return result
}

func readStdIn() {
	mergeChan := make(chan mapData, Go*AddSizeChan)
	mergeGroup := &sync.WaitGroup{}
	resultChan := make(chan mapData, Go*AddSizeChan)
	resultGroup := new(sync.WaitGroup)

	for i := 0; i < Go; i++ {
		go goMergeData(mergeChan, resultChan, mergeGroup)
	}
	go goPrettyPrint(resultChan, resultGroup) // Горутина для объеденения результата 10 потоков

	in := bufio.NewScanner(os.Stdin)
	ParsStream(in, BuildChain(readConf()), mergeChan)

	close(mergeChan)
	mergeGroup.Wait()
	close(resultChan)
	resultGroup.Wait()
}

func ParsStream(Scan *bufio.Scanner, RepChain *Chain, mergeChan chan<- mapData) {

	inChan := make(chan *string, Go*2) // канал в который будет писаться исходные данные для парсенга
	WriteGroup := &sync.WaitGroup{}    // Група для ожидания завершения горутин работающих с каналом inChan
	//RepChain := ChainPool.Get().(*Chain) // Объект который будет парсить

	pattern := `(?mi)\d\d:\d\d\.\d+[-]\d+`
	re := regexp.MustCompile(pattern)

	for i := 0; i < Go; i++ {
		go startWorker(inChan, mergeChan, WriteGroup, RepChain)
	}

	buff := make([]string, 1)
	PushChan := func() {
		part := strings.Join(buff, "\n")
		inChan <- &part
		buff = nil // Очищаем
	}

	for ok := Scan.Scan(); ok == true; {
		txt := Scan.Text()
		if ok := re.MatchString(txt); ok {
			PushChan()
			buff = append(buff, txt)
		} else {
			// Если мы в этом блоке, значит у нас многострочное событие, накапливаем строки в буфер
			buff = append(buff, txt)
		}

		ok = Scan.Scan()
	}
	if len(buff) > 0 {
		PushChan()
	}

	close(inChan) // Закрываем канал на для чтения
	WriteGroup.Wait()
}

func PrettyPrint(inData mapData) {
	//fmt.Print("\n============================\n\n")

	// переводим map в массив
	len := len(inData)
	array := make([]*Data, len, len)
	i := 0
	for _, value := range inData {
		array[i] = value
		i++
	}

	Top = int(math.Min(float64(Top), float64(len)))
	if SortByCount {
		SortCount := func(i, j int) bool { return array[i].count > array[j].count }
		sort.Slice(array, SortCount)
	} else if SortByValue {
		SortValue := func(i, j int) bool { return array[i].value > array[j].value }
		sort.Slice(array, SortValue)
	}

	/* for k, v := range inData {
		fmt.Println("Ключ: ", k, "\n", "Значение", v.OutStr)
	} */

	for id := range array[:Top] {
		OutStr := array[id].OutStr
		OutStr = strings.Replace(OutStr, "%count%", fmt.Sprintf("%d", array[id].count), -1)
		OutStr = strings.Replace(OutStr, "%Value%", fmt.Sprintf("%d", array[id].value), -1)

		fmt.Println(OutStr + "\n")
	}
}

func ParsPart(Blob *string, RepChain IChain) mapData {
	Str := *Blob
	if Str == "" {
		return nil
	}
	//return make(mapData)

	key, data, value := RepChain.Execute(Str)
	/* 	key := ""
	   	data := ""
	   	var value int64 = 0 */
	result := Data{OutStr: data, value: value, count: 1}
	return mapData{GetHash(key): &result}
}

//////////////////////////// Профилирование //////////////////////////////////

func StartCPUProf() {
	f, err := os.Create("cpu.out")
	//defer f.Close() нельзя иначе писаться не будет

	if err != nil {
		fmt.Println("Произошла ошибка при создании cpu.out: ", err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		fmt.Println("Не удалось запустить профилирование CPU: ", err)
	}
}

func StartMemProf() {
	f, err := os.Create("mem.out")
	//defer f.Close()

	if err != nil {
		fmt.Println("Произошла ошибка при создании mem.out: ", err)
	}

	runtime.GC() // get up-to-date statistics
	if err := pprof.WriteHeapProfile(f); err != nil {
		fmt.Println("Не удалось запустить профилирование памяти: ", err)
	}
}

//////////////////////////////////////////////////////////////////////////////

//////////////////////////// Горутины ////////////////////////////////////////

func ReadInfoChan(infoChan <-chan int64, Fullsize *int64) {
	for size := range infoChan {
		fmt.Printf("\nОбработано %f%v", float64(size)/float64(*Fullsize)*100, `%`)
		//*Fullsize = atomic.AddInt64(Fullsize, -size)
	}
}

func goMergeData(outChan <-chan mapData, resultChan chan<- mapData, G *sync.WaitGroup) {
	G.Add(1)
	defer G.Done()

	var Data = make(mapData)
	for input := range outChan {
		Data.MergeData(input)
		runtime.Gosched() // Передаем управление другой горутине.
	}

	resultChan <- Data
}

func goPrettyPrint(resultChan <-chan mapData, G *sync.WaitGroup) {
	G.Add(1)
	defer G.Done()

	var resultData = make(mapData)
	for input := range resultChan {
		resultData.MergeData(input)
	}

	PrettyPrint(resultData)
}

func startWorker(inChan <-chan *string, outChan chan<- mapData, group *sync.WaitGroup, Chain *Chain) {
	group.Add(1)
	defer group.Done()

	for input := range inChan {
		outChan <- ParsPart(input, Chain)
		runtime.Gosched() // Передаем управление другой горутине.
	}
}

func ParsFile(FilePath string, mergeChan chan<- mapData, Chain *Chain, group *sync.WaitGroup) {
	defer group.Done()

	var file *os.File
	defer file.Close()
	file, er := os.Open(FilePath)
	/*info, _ := file.Stat()
	 if v {
		defer func() { infoChan <- info.Size() }()
	} */

	if er != nil {
		fmt.Printf("Ошибка открытия файла %q\n\t%v", FilePath, er.Error())
		return
	}

	ParsStream(bufio.NewScanner(file), Chain, mergeChan)
}

//////////////////////////////////////////////////////////////////////////////

///////////////////////// Сериализация ///////////////////////////////////////

func (d *mapData) SerializationAndSave(TempDir string) {

	file, err2 := os.Create(path.Join(TempDir, Uuid()))
	defer file.Close()
	if err2 != nil {
		fmt.Println("Ошибка создания файла:\n", err2.Error())
		return
	}

	d.Serialization(file)
}

func (d *mapData) Serialization(outFile *os.File) {
	Encode := gob.NewEncoder(outFile)

	err := Encode.Encode(d)
	if err != nil {
		fmt.Println("Ошибка создания Encode:\n", err.Error())
		return
	}
}

func deSerialization(filePath string) (mapData, error) {
	var file *os.File
	file, err := os.Open(filePath)
	defer os.Remove(filePath)
	defer file.Close()

	if err != nil {
		fmt.Printf("Ошибка открытия файла %q:\n\t%v", filePath, err.Error())
		return nil, err
	}

	var Data mapData
	Decoder := gob.NewDecoder(file)

	err = Decoder.Decode(&Data)
	if err != nil {
		fmt.Printf("Ошибка десериализации:\n\t%v", err.Error())
		return nil, err
	}

	return Data, nil
}

//////////////////////////////////////////////////////////////////////////////

//////////////////////// Системные методы ////////////////////////////////////

func (t *ProfTime) Start() *ProfTime {
	t.StartTime = time.Now()
	return t
}

func (t *ProfTime) Stop() {
	diff := time.Now().Sub(t.StartTime)
	fmt.Printf("Код выполнялся: %v\n", diff)
}

func (this mapData) MergeData(inData mapData) {
	for k, value := range inData {
		if _, exist := this[k]; exist {
			this[k].value += value.value
			this[k].count += value.count
		} else {
			this[k] = value
		}
	}
}

func (this mapData) GetObject() mapData {
	return this
}

func GetFiles(DirPath string) ([]string, int64) {
	var result []string
	var size int64
	f := func(path string, info os.FileInfo, err error) error {
		if info.IsDir() || info.Size() == 0 {
			return nil
		} else {
			result = append(result, path)
			size += info.Size()
		}

		return nil
	}

	filepath.Walk(DirPath, f)
	return result, size
}

func FindFiles(rootDir string) {
	if _, err := os.Stat(rootDir); os.IsNotExist(err) {
		panic(fmt.Sprintf("Файл %q не существует", rootDir))
	}

	group := new(sync.WaitGroup)                    // Группа для горутин по файлам
	mergeGroup := new(sync.WaitGroup)               // Группа для горутин которые делают первичное объеденение
	mergeChan := make(chan mapData, Go*AddSizeChan) // Канал в который будут помещаться данные от пула воркеров, для объеденения
	resultChan := make(chan mapData, Go*AddSizeChan)
	resultGroup := new(sync.WaitGroup)
	//infoChan := make(chan int64, 2)                 // Информационный канал, в него пишется размеры файлов
	Files, size := GetFiles(rootDir)
	Chain := BuildChain(readConf())

	for i := 0; i < Go; i++ {
		go goMergeData(mergeChan, resultChan, mergeGroup)
	}
	go goPrettyPrint(resultChan, resultGroup) // Горутина для объеденения результата 10 потоков
	//if v {
	//	go ReadInfoChan(infoChan, &size)
	//}

	if v {
		fmt.Printf("Поиск файлов в каталоге %q, общий размер (%v kb)", rootDir, size/1024)
	}

	for _, File := range Files {
		if strings.HasSuffix(File, "log") {
			group.Add(1)
			go ParsFile(File, mergeChan, Chain, group)
		}
	}

	group.Wait()
	close(mergeChan)
	//close(infoChan)
	mergeGroup.Wait()
	close(resultChan)
	resultGroup.Wait()
}

//////////////////////////////////////////////////////////////////////////////

///////////////////////// Legacy ///////////////////////////////////////////

func MergeFiles(DirPath string) {
	commonData := make(mapData)
	Files, _ := GetFiles(DirPath)
	for _, filePath := range Files {
		if Data, er := deSerialization(filePath); er == nil {
			commonData.MergeData(Data)
		}
	}

	commonData.SerializationAndSave(DirPath)

}

func MergeDirs(Dirs []string) string {
	commonData := make(mapData)

	for _, dir := range Dirs {
		Files, _ := GetFiles(dir)
		for _, file := range Files {
			if Data, er := deSerialization(file); er == nil {
				commonData.MergeData(Data)
			}
		}
		os.RemoveAll(dir)
	}
	TempDir, _ := ioutil.TempDir("", "")
	commonData.SerializationAndSave(TempDir)
	return TempDir
}

func goReaderDirChan(dirChan <-chan string, G *sync.WaitGroup) {
	defer G.Done()

	var Dirs []string
	for dir := range dirChan {
		Dirs = append(Dirs, dir)
	}
	MergeDirs(Dirs)
}

//////////////////////////////////////////////////////////////////////////////
