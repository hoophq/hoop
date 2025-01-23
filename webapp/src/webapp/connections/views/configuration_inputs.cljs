(ns webapp.connections.views.configuration-inputs
  (:require [clojure.string :as cs]
            [reagent.core :as r]
            [webapp.components.forms :as forms]
            [webapp.connections.helpers :as helpers]))


(defn- config->inputs-labeled
  [{:keys [key value required placeholder hidden]} index config]
  (let [key-val (r/atom key)
        value-val (r/atom value)
        save (fn [k v] (swap! config assoc-in [index k] v))]
    (fn []
      [:<>
       [forms/input {:classes "whitespace-pre overflow-x"
                     :on-change #(reset! value-val (-> % .-target .-value))
                     :on-blur #(save :value @value-val)
                     :label (cs/lower-case (cs/replace @key-val #"_" " "))
                     :required required
                     :placeholder (or placeholder key)
                     :type "password"
                     :hidden hidden
                     :value @value-val}]])))

(defn- config->inputs-files
  [{:keys [key value]} index config]
  (let [key-val (r/atom key)
        value-val (r/atom value)
        save (fn [k v] (swap! config assoc-in [index k] v))]
    (fn []
      [:<>
       [forms/input {:label "Name"
                     :id (str "file-name" @key-val)
                     :classes "whitespace-pre overflow-x"
                     :placeholder "kubeconfig"
                     :on-change #(helpers/parse->posix-format % key-val)
                     :on-blur #(save :key @key-val)
                     :value @key-val}]
       [forms/textarea {:label "Content"
                        :id (str "file-content" @value-val)
                        :placeholder "Paste your file content here"
                        :on-change #(reset! value-val (-> % .-target .-value))
                        :on-blur #(save :value @value-val)
                        :value @value-val}]])))

(defn- config->inputs
  [{:keys [key value]} index config {:keys [is-disabled? is-required?]}]
  (let [key-val (r/atom key)
        value-val (r/atom value)
        save (fn [k v] (swap! config assoc-in [index k] v))]
    (fn []
      [:<>
       [forms/input {:label "Key"
                     :classes "whitespace-pre overflow-x"
                     :on-change #(helpers/parse->posix-format % key-val)
                     :on-blur #(save :key @key-val)
                     :disabled is-disabled?
                     :required is-required?
                     :value @key-val
                     :placeholder "API_KEY"}]
       [forms/input {:label "Value"
                     :classes "whitespace-pre overflow-x"
                     :on-change #(reset! value-val (-> % .-target .-value))
                     :on-blur #(save :value @value-val)
                     :type "password"
                     :required is-required?
                     :value @value-val
                     :placeholder "* * * *"}]])))

(defn config-inputs-labeled
  [config attr]
  (doall
   (for [index (range (count @config))]
     ^{:key (str (get @config index) "labeled")}
     [config->inputs-labeled (get @config index) index config attr])))

(defn config-inputs-files
  [config attr]
  (doall
   (for [index (range (count @config))]
     ^{:key (str (get @config index) "-file")}
     [config->inputs-files (get @config index) index config attr])))

(defn config-inputs
  [config attr]
  (doall
   (for [index (range (count @config))]
     ^{:key (str (get @config index) "-config")}
     [config->inputs (get @config index) index config attr])))
