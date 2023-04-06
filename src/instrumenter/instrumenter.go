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
instrumenter.go
instrument files to work with the
"github.com/ErikKassubek/GoChan/goChan" libraries
*/

import (
	"fmt"
	"go/parser"
	"go/printer"
	"go/token"
	"io/ioutil"
	"os"
	"strings"
	"fyne.io/fyne/v2/widget"
	"fyne.io/fyne/v2/container"
)

/*
Function to perform instrumentation of all list of files
@param file_paths []string: list of file names to instrument
@return string: name of exec
@return error: error or nil
*/
func instrument_files(file_paths []string, output *widget.TextGrid,
	outputScroll *container.Scroll, 
	progress *widget.ProgressBar) (string, error) {
	execName = ""
	for i, file := range file_paths {
		output.SetText(output.Text() + fmt.Sprintf("Instrumenting file %s.\n", file))
		outputScroll.ScrollToBottom()
		en, err := instrument_file(file)
		if en != "" {
			execName = en
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to instrument file %s.\n", file)
			return execName, err
		}
		prog := float64(i + 1) / float64(len(file_paths))
		progress.SetValue(prog)
	}
	output.SetText(output.Text() + "Instrumentation complete.\n")
	outputScroll.ScrollToBottom()
	return execName, nil
}

/*
Function to instrument a given file.
@param file_path string: path to the file
@return string: name of exec
@return error: error or nil
*/
func instrument_file(file_path string) (string, error) {
	// create output file
	output_file, err := os.Create(out + file_path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create output file %s.\n", out+file_path)
		return "", err
	}
	defer output_file.Close()

	// copy mod and sum files
	if file_path[len(file_path)-3:] != ".go" {
		execName := ""
		content, err := ioutil.ReadFile(file_path)
		if file_path[len(file_path)-4:] == ".mod" {
			execName = strings.Split(strings.Split(string(content), "\n")[0], " ")[1]
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to read file %s.\n", file_path)
			return "", err
		}
		_, err = output_file.Write(content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write to output file %s.\n", out+file_path)
			return "", err
		}
		return execName, nil
	}

	// instrument go files
	err = instrument_go_file(file_path)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not instrument %s\n", file_path)
	}

	return "", nil
}

/*
Function to instrument a go file.
@param file_path string: path to the file
@return error: error or nil
*/
func instrument_go_file(file_path string) error {
	// get the ASP of the file
	astSet := token.NewFileSet()

	f, err := parser.ParseFile(astSet, file_path, nil, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not parse file %s\n", file_path)
		return err
	}

	instrument_chan(f, astSet)
	instrument_mutex(f)

	// print changed ast to output file
	output_file, err := os.OpenFile(out+file_path, os.O_WRONLY, os.ModePerm)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not open output file %s\n", out+file_path)
		return err
	}
	defer output_file.Close()

	if err := printer.Fprint(output_file, astSet, f); err != nil {
		return err
	}

	return nil
}