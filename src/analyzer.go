package main

import "fmt"

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
Package: dedego-analyzer (Deadlock Detector in Go)
Project: Dynamic Analysis to detect potential deadlocks in
*/

/*
analyzer.go
Main functions to start the analyzer
*/

/*
Main function to run the analyzer. The running of the analyzer locks
tracesLock for the total duration of its runtime, to prevent go-routines,
that are still running when the main function terminated (and therefore would
normally also be terminated) to alter the trace.
*/
func RunAnalyzer(traces *([][]TraceElement)) {
	res := make([]string, 0) // result

	analyzeMutexDeadlock(traces, &res)

	// vcTrace, bc, b := buildVectorClockChan()

	// rs, s, r := findAlternativeCommunication(vcTrace, bc, b)

	// rsComm := findPossibleInvalidCommunications(rs, vcTrace, s, r)
	// resString = append(resString, rsComm)

	// _, rsClose := checkForPossibleSendToClosed(vcTrace)
	// resString = append(resString, rsClose...)

	// // print res Strings
	for _, prob := range res {
		fmt.Println(prob)
	}
}
