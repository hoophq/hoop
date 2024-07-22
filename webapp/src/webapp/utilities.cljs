(ns webapp.utilities
  (:require [clojure.string :as string]))

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

(defn classes-of
  "Get the classes of an element as a Clojure keyword vector."
  [e]
  (let [words (-> e (.getAttribute "class") (string/split " "))]
    (mapv keyword words)))

(defn classes->str
  "Change a Clojure keyword seq into an HTML class string."
  [classes]
  (->> classes (mapv name) (string/join " ")))

(defn class-reset!
  "Unconditionally set the classes of an element."
  [e classes]
  (.setAttribute e "class" (classes->str classes))
  e)

(defn class-swap!
  "Update the classes of an element using a fn."
  [e f]
  (class-reset! e (f (classes-of e))))

(defn add-class!
  "Add a class to an element."
  [e class]
  (class-swap! e #(distinct (conj % (keyword class)))))

(defn remove-class!
  "Remove a class from an element."
  [e class]
  (class-swap! e (fn [current] (remove #(= % (keyword class)) current))))
