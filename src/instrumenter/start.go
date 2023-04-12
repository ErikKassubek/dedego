package instrumenter

/*
Copyright (c) 2023, Erik Kassubek
All rights reserved.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
*/

/*
Author: Erik Kassubek <erik-kassubek@t-online.de>
Package: GoChan-Instrumenter
Project: Bachelor Thesis at the Albert-Ludwigs-University Freiburg,
	Institute of Computer Science: Dynamic Analysis of message passing go programs
*/

/*
main.go
main function and handling of command line arguments
*/

import (
	"os"
	"os/exec"

	"github.com/ErikKassubek/deadlockDetectorGo/src/gui"
)

const MAX_TOTAL_WAITING_TIME_SEC = "20"
const SELECT_WAITING_TIME string = "2 * time.Second"

var out string = os.TempDir() + string(os.PathSeparator) + "dedego"

func Run(path string, elements *gui.GuiElements, status *gui.Status) error {
	elements.AddToOutput("Starting Instrumentation\n")

	fileName, err := getAllFiles(path, status)
	if err != nil {
		elements.AddToOutput("Failed to get all files\n")
		return err
	}

	// instrument all files in file_names
	err = instrument_files(fileName, elements, status)
	if err != nil {
		elements.AddToOutput("Instrumentation Failed: " + err.Error())
		return err
	} else {
		elements.AddToOutput("Instrumentation Complete\n")
	}

	// build the instrumented program
	// cmd := exec.Command("go", "build", "-o", status.Name)

	// install analyzer
	elements.AddToOutput("Installing Analyzer\n")
	elements.ProgressBuild.SetValue(0.1)
	cmd := exec.Command("go", "get",
		"github.com/ErikKassubek/deadlockDetectorGo/src/dedego")
	cmd.Dir = out + string(os.PathSeparator) + status.Name
	out, err := cmd.Output()
	elements.ProgressBuild.SetValue(0.25)
	elements.AddToOutput(string(out) + "\n")
	if err != nil {
		elements.AddToOutput("Failed to install Analyzer: " + err.Error())
		return err
	}

	// TODO: build program
	elements.ProgressBuild.SetValue(1)

	if err != nil {
		elements.AddToOutput("Build Failed: " + err.Error())
		return err
	} else {
		elements.AddToOutput("Build Complete.\n")
	}

	// // create the new main file

	// // read template
	// dat, err := os.ReadFile("./instrumenter/main_template.txt")
	// if err != nil {
	// 	dat, err = os.ReadFile("./main_template.txt")
	// 	if err != nil {
	// 		panic(err)
	// 	}
	// }
	// data := string(dat)

	// path := ""

	// for _, f := range file_name {
	// 	file_path_split := strings.Split(f, path_separator)
	// 	if file_path_split[len(file_path_split)-1] == "main.go" {
	// 		path = f
	// 	}
	// }

	// if path == "" {
	// 	panic("Could not find main file!")
	// }

	// path = strings.Replace(path, "main.go", execName, -1)

	// // replace placeholder
	// data = strings.Replace(data, "$$COMMAND$$", "./"+path, -1)

	// save_size := ""
	// for _, sw := range select_ops {
	// 	save_size += "switch_size[" + fmt.Sprint(sw.id) + "] = " + fmt.Sprint(sw.size) + "\n"
	// }

	// data = strings.Replace(data, "$$SWITCH_SIZE$$", save_size, -1)

	// f, err := os.Create(out + "main.go")
	// if err != nil {
	// 	panic(err)
	// }
	// defer f.Close()

	// f.Write([]byte(data))

	return nil
}
