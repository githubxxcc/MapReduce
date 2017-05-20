package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	mr "mapreduce"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	d = 0.85
	N = 20000
)

// The mapping function is called once for each piece of the input. In this
// framework, the key is the name of the file that is being processed, and the
// value is the file's contents. The return value should be a slice of key/value
// pairs, each represented by a mapreduce.KeyValue.
// Input: fileName, fileContent
// Ouput: Key: v, Value: PR/n
func mapF(fileName string, contents string) (res []mr.KeyValue) {
	lines := strings.Split(contents, "\n")
	for _, line := range lines {
		if len(line) > 0 {
			p, PRp, vs := parseInputLine(line)

			//lp as the number of outbound links for page p
			lp := len(vs)
			for _, v := range vs {
				PRv := calculatePR(PRp, lp)
				res = append(res, mr.KeyValue{v, PRv})
			}

			//add the dump factor for itself
			dumpP := strconv.FormatFloat((1-d)/(N*d), 'f', -1, 64)
			res = append(res, mr.KeyValue{p, dumpP})
		}
	}
	fmt.Printf("Map sending [%v]\n", res)
	return
}

func parseInputLine(line string) (string, string, []string) {
	tokens := strings.Split(line, ": ")
	fmt.Printf("Line:\n%s\n", line)
	p := strings.TrimSpace(tokens[0])

	prpAndVs := strings.SplitN(tokens[1], ", ", 2)
	prp := strings.TrimSpace(prpAndVs[0])

	vs := strings.Split(prpAndVs[1], ", ")
	return p, prp, vs
}

func calculatePR(pr string, L int) string {
	val, err := strconv.ParseFloat(pr, 64)
	checkErr(err, "Cannot parse value to float: ", pr)
	return strconv.FormatFloat(val/float64(L), 'f', -1, 64)
}

// The reduce function is called once for each key generated by Map, with a list
// of that key's string value (merged across all inputs). The return value
// should be a single output value for that key.
// Input: Key - v(outpage) , Values PRs from each p
// Output: sum(PRs)
func reduceF(key string, values []string) (res string) {
	newPr := reducePRs(values)
	res = strconv.FormatFloat(newPr, 'f', -1, 64)
	fmt.Printf("Reduce sending [%v]\n -----from [%v]\n", res, values)
	return
}

func reducePRs(values []string) float64 {
	var sumPr float64

	for _, valstr := range values {
		val, err := strconv.ParseFloat(valstr, 64)
		checkErr(err, "Failed to parse value at reduceF: ", valstr)

		sumPr += val
	}

	return d * sumPr
}

// Parses the command line arguments and runs the computation.
func main() {
	// TODO: Implement this function for Page Rank. You won't be able to use
	// `run` directly here since you'll need to iterative compute the page rank.
	// See mr/parse_cmd_line.go for an example on how to use the library
	// directly. Ignore the node type: you can choose what you do.

	// Some useful code, to get started:
	jobName := "pagerank"
	numIterations := 10

	_, reducers, inputFileNames := mr.ParseCmdLine()

	//Process the inputfiles, to get the outbound links for each page
	pageLinks := processLinks(inputFileNames)
	inputFileNames = copyInputs(inputFileNames)

	// numMappers equal to numInputFiles
	numMappers := len(inputFileNames)
	done := make(chan bool)
	for i := 0; i < numIterations; i++ {
		//Set Up
		// Always use Parallel master
		master := mr.NewParallelMaster(jobName, inputFileNames, reducers, mapF, reduceF)
		setupMaster(master, done)
		registerWorkers(numMappers, jobName, done)

		//Do Map, Do Reduce, Do Merge
		tempOutputFile := master.Merge()
		//Update input files
		updateInputs(inputFileNames, tempOutputFile, pageLinks)
		cleanUp(jobName, int(reducers), numMappers)
	}

}

func setupMaster(master *mr.ParallelMaster, done chan bool) {
	go func() {
		master.Start()
		done <- true
	}()
}

func copyInputs(fNames []string) []string {
	fileCopies := make([]string, 0, len(fNames))
	err := os.Mkdir(fmt.Sprintf("%s/tmp", mr.DataInputDir), 0777)
	checkErr(err, "Failed to create tmp direcotry for input")
	for _, fN := range fNames {
		src, err := os.Open(fN)
		fCopy := inputCopyName(fN)
		dst, err := os.Create(fCopy)

		_, err = io.Copy(dst, src)
		checkErr(err, fmt.Sprintf("Failed to copy input files %s", fN))
		src.Close()
		dst.Close()
		fileCopies = append(fileCopies, fCopy)
	}

	return fileCopies
}

func inputCopyName(o string) string {
	dir, fileN := filepath.Split(o)
	return fmt.Sprintf("%stmp/%s", dir, fileN)
}

//Ack: Based on test_test.go
func registerWorkers(numMappers int, job string, done chan bool) {

	// Make sure the master (probably) sets up so workers can register quickly.
	runtime.Gosched()
	time.Sleep(100 * time.Millisecond)
	runtime.Gosched()

	workers := make([]*mr.Worker, 0, numMappers)
	for i := 0; i < numMappers; i++ {
		worker := mr.NewWorker(job, mapF, reduceF)
		workers = append(workers, worker)
		go worker.Start()
	}
	<-done
}

func processLinks(inputs []string) map[string]string {
	links := make(map[string]string)

	for _, fileName := range inputs {
		f, err := os.Open(fileName)
		checkErr(err, "Failed to open file for preocessing links: ", fileName)
		defer f.Close()
		sc := bufio.NewScanner(f)

		for sc.Scan() {
			line := sc.Text()
			p, _, vs := parseInputLine(line)
			links[p] = strings.Join(vs, ", ")
		}
	}

	return links
}

func updateInputs(inputFileNames []string, tempOutputFile string, pageLinks map[string]string) {

	curPRs := make(map[string]string)

	//Read Current PRs
	file, err := os.Open(tempOutputFile)
	checkErr(err, "Failed open temp output file")

	sc := bufio.NewScanner(file)
	for sc.Scan() {
		line := sc.Text()
		tokens := strings.Split(line, ": ")
		curPRs[tokens[0]] = tokens[1]
	}

	file.Close()

	for _, fileName := range inputFileNames {
		var buffer bytes.Buffer
		//Open File
		inputFile, err := os.Open(fileName)
		checkErr(err, "Failed to open input file: ", fileName)

		//Read file line by line
		sc = bufio.NewScanner(inputFile)
		for sc.Scan() {
			line := sc.Text()
			pid, _, _ := parseInputLine(line)
			//Write to buffer
			buffer.WriteString(fmt.Sprintf("%s: %s, %s\n", pid, curPRs[pid], pageLinks[pid]))
		}

		inputFile.Close()
		//Overwrite the file
		err = ioutil.WriteFile(fileName, buffer.Bytes(), 0664)
		checkErr(err, "Failed to write to input file: ", fileName)
	}
}

func cleanUp(jobName string, numReducers, numMappers int) {
	for i := 0; i < numReducers; i++ {
		for k := 0; k < numMappers; k++ {
			fN := reduceInputName(jobName, k, i)
			err := os.Truncate(fN, 0)
			checkErr(err, "Cannot Truncate file : ", fN)
		}
	}
}

func reduceInputName(jobName string, mapperNum, reducerNum int) string {
	return mr.DataOutputDir + "mr." + jobName + "-" +
		strconv.Itoa(mapperNum) + "-" + strconv.Itoa(reducerNum)

}

// A convenience function. Checks whether some error is nil. If it not, i.e.,
// there is an error, panics with the error along with the message `msg`.
func checkErr(err error, msg ...string) {
	if err != nil {
		var buffer bytes.Buffer
		for _, m := range msg {
			buffer.WriteString(m)
		}
		panicMessage := fmt.Sprintf("Error: %s\n%v", err, buffer.String())
		panic(panicMessage)
	}
}
