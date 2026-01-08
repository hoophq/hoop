(ns webapp.utilities
  (:require [clojure.string :as string]
            ["clsx" :refer [clsx]]
            ["tailwind-merge" :refer [twMerge]]))

(defn get-url-params
  "Gets the URL params in the URL and format in a hashmap {\"key\" \"value\"}"
  []
  (let [search (.. js/window -location -search)
        url-search-params (new js/URLSearchParams search)
        url-params-list (js->clj (for [q url-search-params] q))
        url-params-map (into (sorted-map) url-params-list)]
    url-params-map))

(defn sanitize-string [s]
  (let [special-words #{"of" "the" "and"}
        capitalize (fn [word]
                     (if (contains? special-words (clojure.string/lower-case word))
                       (clojure.string/lower-case word)
                       (str (clojure.string/capitalize word))))]
    (->> (clojure.string/split s #"_|-")
         (map capitalize)
         (clojure.string/join " "))))

(defn cn [& inputs]
  (twMerge (apply clsx inputs)))

(defn decode-b64 [data]
  (try
    (let [decoded (js/atob data)]
      (try
        (string/replace (js/decodeURIComponent (js/escape decoded)) #"∞" "\t")
        (catch js/Error _ decoded)))
    (catch js/Error _ "")))
