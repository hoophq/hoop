(ns webapp.features.runbooks.helpers
  (:require
   [clojure.string :as cs]))

(defn extract-repo-name
  "Extract just the repository name from a full repository path.
   Example: 'github.com/hoophq/runbooks' -> 'runbooks'"
  [repo-path]
  (when
   (string? repo-path)
    (->> (cs/split repo-path #"/") (remove cs/blank?) last)))
