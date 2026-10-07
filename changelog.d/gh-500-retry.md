level: patch

`workspace merge --wait` rides out a GitHub 500 on its status reads, including the paginated check-runs read that gh reports as "unexpected end of JSON input", with the same bounded backoff as a dropped connection.
