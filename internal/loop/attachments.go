package loop

import (
	"path"

	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/store"
)

// WorkstationFilesDir is where a containerized workstation is given the
// files its messages carry: inside the loop's volume, so a file outlives
// the wake it arrived in, and under a dot-directory, so it stays out of the
// loop's own work.
const WorkstationFilesDir = runtime.WorkstationHome + "/.spool/files"

// NotKeptRemoved is why a kept file is gone: retention removed it. It sits
// beside the store's NotKept reasons, which are why a file never arrived.
const NotKeptRemoved = "removed"

// PresentAttachments is what loopRecord is shown of a message's attachments,
// and the copies its workstation needs first. hostPath maps a kept file to
// the hub's copy.
//
// A bare loop runs on the host, so it is shown the hub's copy and needs no
// copy made. Any other runtime's workstation has a filesystem of its own:
// the file is named at WorkstationFilesDir, and copied there before the
// turn that reads it.
func PresentAttachments(loopRecord *store.Loop, attachments []*store.Attachment, hostPath func(rel string) string) ([]Attachment, []FileCopy) {
	var shown []Attachment
	var copies []FileCopy
	for _, attachment := range attachments {
		item := Attachment{
			Kind:   attachment.Kind,
			Name:   attachment.Name,
			Size:   attachment.Size,
			Width:  attachment.Width,
			Height: attachment.Height,
		}
		switch {
		case attachment.Path == "":
			item.NotKept = attachment.NotKept
		case attachment.RemovedAt != 0:
			item.NotKept = NotKeptRemoved
		case loopRecord.Runtime == store.RuntimeBare || loopRecord.Runtime == "":
			item.Path = hostPath(attachment.Path)
		default:
			item.Path = path.Join(WorkstationFilesDir, attachment.Path)
			copies = append(copies, FileCopy{From: hostPath(attachment.Path), To: item.Path})
		}
		shown = append(shown, item)
	}
	return shown, copies
}
