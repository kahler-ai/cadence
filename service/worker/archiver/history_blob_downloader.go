// Copyright (c) 2017 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package archiver

import (
	"context"
	"encoding/json"
	"github.com/uber/cadence/common"
	"github.com/uber/cadence/common/blobstore"
	"github.com/uber/cadence/common/blobstore/blob"
)

type (
	// DownloadPageRequest is request to DownloadPage
	DownloadPageRequest struct {
		NextPageToken        []byte
		ArchivalBucket       string
		DomainID             string
		WorkflowID           string
		RunID                string
		CloseFailoverVersion *int64
	}

	// DownloadPageResponse is response from DownloadPage
	DownloadPageResponse struct {
		NextPageToken []byte
		HistoryBlob   *HistoryBlob
	}

	// HistoryBlobDownloader is used to download history blobs
	HistoryBlobDownloader interface {
		DownloadBlob(context.Context, *DownloadPageRequest) (*DownloadPageResponse, error)
	}

	historyBlobDownloader struct {
		blobstoreClient blobstore.Client
	}

	archivalToken struct {
		BlobstorePageToken   int
		CloseFailoverVersion int64
	}
)

// NewHistoryBlobDownloader returns a new HistoryBlobDownloader
func NewHistoryBlobDownloader(blobstoreClient blobstore.Client) HistoryBlobDownloader {
	return &historyBlobDownloader{
		blobstoreClient: blobstoreClient,
	}
}

// DownloadBlob is used to access a history blob from blobstore.
// CloseFailoverVersion can be optionally provided to get a specific version of a blob, if not provided gets highest available version.
func (d *historyBlobDownloader) DownloadBlob(ctx context.Context, request *DownloadPageRequest) (*DownloadPageResponse, error) {
	var token *archivalToken
	var err error
	if request.NextPageToken != nil {
		token, err = deserializeHistoryTokenArchival(request.NextPageToken)
		if err != nil {
			return nil, err
		}
	} else if request.CloseFailoverVersion != nil {
		token = &archivalToken{
			BlobstorePageToken:   common.FirstBlobPageToken,
			CloseFailoverVersion: *request.CloseFailoverVersion,
		}
	} else {
		indexKey, err := NewHistoryIndexBlobKey(request.DomainID, request.WorkflowID, request.RunID)
		if err != nil {
			return nil, err
		}
		indexTags, err := d.blobstoreClient.GetTags(ctx, request.ArchivalBucket, indexKey)
		if err != nil {
			return nil, err
		}
		highestVersion, err := GetHighestVersion(indexTags)
		if err != nil {
			return nil, err
		}
		token = &archivalToken{
			BlobstorePageToken:   common.FirstBlobPageToken,
			CloseFailoverVersion: *highestVersion,
		}
	}
	key, err := NewHistoryBlobKey(request.DomainID, request.WorkflowID, request.RunID, token.CloseFailoverVersion, token.BlobstorePageToken)
	if err != nil {
		return nil, err
	}
	b, err := d.blobstoreClient.Download(ctx, request.ArchivalBucket, key)
	if err != nil {
		return nil, err
	}
	unwrappedBlob, wrappingLayers, err := blob.Unwrap(b)
	if err != nil {
		return nil, err
	}
	historyBlob := &HistoryBlob{}
	switch *wrappingLayers.EncodingFormat {
	case blob.JSONEncoding:
		if err := json.Unmarshal(unwrappedBlob.Body, historyBlob); err != nil {
			return nil, err
		}
	}
	if *historyBlob.Header.IsLast {
		token = nil
	} else {
		token.BlobstorePageToken = *historyBlob.Header.NextPageToken
	}
	nextToken, err := serializeHistoryTokenArchival(token)
	if err != nil {
		return nil, err
	}
	return &DownloadPageResponse{
		NextPageToken: nextToken,
		HistoryBlob:   historyBlob,
	}, nil
}

func deserializeHistoryTokenArchival(bytes []byte) (*archivalToken, error) {
	token := &archivalToken{}
	err := json.Unmarshal(bytes, token)
	return token, err
}

func serializeHistoryTokenArchival(token *archivalToken) ([]byte, error) {
	if token == nil {
		return nil, nil
	}

	bytes, err := json.Marshal(token)
	return bytes, err
}
