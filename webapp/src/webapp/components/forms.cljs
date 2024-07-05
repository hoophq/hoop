(ns webapp.components.forms
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]
            [reagent.core :as r]
            [clojure.string :as cs]))

(defn- form-label
  [text]
  [:label {:class "block text-xs font-semibold text-gray-800"} text])


(defn- form-label-dark
  [text]
  [:label {:class "text-white block text-xs font-semibold"} text])

(defn- form-helper-text
  [text dark]
  [:div {:class (str "relative flex flex-col group"
                     (when dark " text-white"))}
   [:> hero-outline-icon/QuestionMarkCircleIcon {:class "w-4 h-4"
                                                 :aria-hidden "true"}]
   [:div {:class "absolute bottom-0 flex-col hidden mb-6 w-max group-hover:flex"}
    [:span {:class (str "relative border -left-3 border-gray-300 bg-white rounded-md z-50 "
                        "p-2 text-xs text-gray-700 leading-none whitespace-no-wrap shadow-lg")}
     text]
    [:div {:class "w-3 h-3 -mt-2 border-r border-b border-gray-300 bg-white transform rotate-45"}]]])

(defonce common-styles
  "relative block w-full
   py-3 px-2
   border border-gray-300 rounded-md
   focus:ring-indigo-500 focus:border-indigo-500 sm:text-sm disabled:opacity-50")

(defonce common-styles-dark
  "relative block w-full bg-transparent
   py-3 px-2 text-white
   border border-gray-400 rounded-md
   focus:ring-white focus:border-white sm:text-sm disabled:opacity-50")

(defonce input-styles
  (str common-styles " h-12"))

(defonce input-styles-dark
  (str common-styles-dark " h-12"))

(defn input
  "Multi purpose HTML input component.
  Props signature:
  :label -> html label text;
  :placeholder -> html prop placeholder for input;
  :value -> a reagent atom piece of state."
  [_]
  (fn
    [{:keys [type on-blur on-focus]}]
    (let [local-type (r/atom (or type "text"))
          on-blur-cb (fn []
                       (when (= type "password")
                         (reset! local-type "password"))
                       (when on-blur (on-blur)))
          on-focus-cb (fn []
                        (when (= type "password")
                          (reset! local-type "text"))
                        (when on-focus (on-focus)))]
      (fn [{:keys [label
                   placeholder
                   name
                   dark
                   id
                   helper-text
                   value
                   defaultValue
                   on-change
                   on-keyDown
                   required
                   full-width?
                   pattern
                   disabled
                   minlength
                   maxlength
                   min
                   max
                   step]}]
        [:div {:class (str "mb-regular text-sm" (when full-width? " w-full"))}
         [:div {:class "flex items-center gap-2 mb-1"}
          (when label
            (if dark
              [form-label-dark label]
              [form-label label]))
          (when (not (cs/blank? helper-text))
            [form-helper-text helper-text dark])]
         [:input
          {:type (if (= "string" @local-type) "text" @local-type)
           :class (if dark
                    input-styles-dark
                    input-styles)
           :id id
           :placeholder (or placeholder label)
           :name name
           :pattern pattern
           :minLength minlength
           :maxLength maxlength
           :min min
           :max max
           :step step
           :value value
           :defaultValue defaultValue
           :on-change on-change
           :on-keyDown on-keyDown
           :on-blur on-blur-cb
           :on-focus on-focus-cb
           :disabled (or disabled false)
           :required (or required false)}]]))))

(defn input-metadata [{:keys [label name id placeholder disabled required value on-change]}]
  [:div {:class "relative"}
   [:label {:htmlFor label
            :class "absolute -top-2 left-2 inline-block bg-white px-1 text-xs font-medium text-gray-900"}
    label]
   [:input {:type "text"
            :name name
            :id id
            :class (str "block w-full rounded-sm bg-gray-800 border-0 py-0 "
                        "text-white shadow-sm ring-1 ring-inset ring-gray-700 "
                        "placeholder-gray-100 placeholder-opacity-40 focus:ring-1 "
                        "focus:ring-inset focus:ring-gray-200 sm:text-xs sm:leading-6")
            :placeholder placeholder
            :disabled (or disabled false)
            :required (or required false)
            :on-change on-change
            :value value}]])

(defn textarea
  [config]
  [:div.mb-regular
   [:div {:class "flex items-center gap-2 mb-1"}
    [form-label (:label config)]
    (when (not (cs/blank? (:helper-text config)))
      [form-helper-text (:helper-text config)])]
   [:textarea
    {:class (str common-styles " " (or (:classes config) ""))
     :id (or (:id config) "")
     :rows (or (:rows config) 5)
     :name (or (:name config) "")
     :value (:value config)
     :defaultValue (:defaultValue config)
     :autoFocus (:autoFocus config)
     :placeholder (:placeholder config)
     :on-change (:on-change config)
     :on-blur (:on-blur config)
     :on-keyDown (:on-keyDown config)
     :disabled (or (:disabled config) false)
     :required (or (:required config) false)}]])

(defn- option
  [item selected]
  (let [attrs {:key (:value item)
               :value (:value item)}]
    [:option attrs (:text item)]))

(defn select
  "HTML select.
  Props signature:
  label -> html label text;
  options -> List of {:text string :value string};
  active -> the option value of an already active item;
  on-change -> function to be executed on change;
  required -> HTML required attribute;"
  [{:keys [label helper-text id name options selected on-change required disabled dark]}]
  [:div {:class "mb-regular text-sm w-full"}
   [:div {:class "flex items-center gap-2 mb-1"}
    (if dark
      [form-label-dark label]
      [form-label label])
    (when (not (cs/blank? helper-text))
      [form-helper-text helper-text dark])]
   [:select
    {:class (if dark
              input-styles-dark
              input-styles)
     :name (or name "")
     :id (or id "")
     :on-change on-change
     :value selected
     :required (or required false)
     :disabled (or disabled false)}
    [:option {:value ""
              :disabled true
              :label "Select one"}]
    (map #(option % selected) options)]])

(defn select-editor
  "HTML select.
  Props signature:
  label -> html label text;
  options -> List of {:text string :value string};
  active -> the option value of an already active item;
  on-change -> function to be executed on change;
  required -> HTML required attribute;"
  [{:keys [id name options selected on-change required disabled]}]
  [:div {:class "text-xs w-24 h-4"}
   [:select
    {:class (str "relative block w-full bg-transparent cursor-pointer "
                 "text-gray-300 border-0 p-0 "
                 "focus:ring-indigo-500 focus:border-indigo-500 text-xs disabled:opacity-50")
     :name (or name "")
     :id (or id "")
     :on-change on-change
     :value selected
     :required (or required false)
     :disabled (or disabled false)}
    [:option {:value ""
              :disabled true
              :label "Select one"}]
    (map #(option % selected) options)]])
