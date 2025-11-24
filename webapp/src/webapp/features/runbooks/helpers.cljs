(ns webapp.features.runbooks.helpers
  (:require
   [clojure.string :as cs]))

(defn extract-repo-name
  "Extract just the repository name from a full repository path.
   Example: 'github.com/hoophq/runbooks' -> 'runbooks'"
  [repo-path]
  (if (string? repo-path)
    (let [parts (cs/split repo-path #"/")]
      (last parts))
    repo-path))
